package debugserver

import (
	"bytes"
	"context"
	"fmt"
	"html/template"
	"io"
	"net/http"
	"os"
	"runtime"
	"runtime/pprof"
	"runtime/trace"
	"slices"
	"strconv"
	"strings"
	"time"
)

const (
	pprofPrefix = "/debug/pprof/"
	// defaultCPUSeconds и defaultTraceSeconds — длительность, если seconds не задан (как в net/http/pprof).
	defaultCPUSeconds   = 30
	defaultTraceSeconds = 1
	// maxSymbolBody ограничивает тело POST /symbol: адреса приходят строкой «0x1+0x2».
	maxSymbolBody = 1 << 20
)

func newMux() *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("GET "+pprofPrefix+"{$}", handleIndex)
	mux.HandleFunc("GET "+pprofPrefix+"cmdline", handleCmdline)
	mux.HandleFunc("GET "+pprofPrefix+"profile", handleCPUProfile)
	mux.HandleFunc("GET "+pprofPrefix+"symbol", handleSymbol)
	mux.HandleFunc("POST "+pprofPrefix+"symbol", handleSymbol)
	mux.HandleFunc("GET "+pprofPrefix+"trace", handleTrace)
	mux.HandleFunc("GET "+pprofPrefix+"{name}", handleNamedProfile)

	return mux
}

// handleIndex отдаёт HTML со списком профилей.
func handleIndex(w http.ResponseWriter, _ *http.Request) {
	type row struct {
		Name  string
		Count int
	}

	profiles := pprof.Profiles()
	rows := make([]row, 0, len(profiles))
	for _, p := range profiles {
		rows = append(rows, row{Name: p.Name(), Count: p.Count()})
	}
	slices.SortFunc(rows, func(a, b row) int { return strings.Compare(a.Name, b.Name) })

	var buf bytes.Buffer
	if err := indexTemplate.Execute(&buf, rows); err != nil {
		http.Error(w, "render index: "+err.Error(), http.StatusInternalServerError)
		return
	}

	setHeaders(w, "text/html; charset=utf-8")
	_, _ = w.Write(buf.Bytes())
}

//nolint:gochecknoglobals // шаблон разбирается один раз при старте
var indexTemplate = template.Must(template.New("index").Parse(`<!doctype html>
<title>/debug/pprof/</title>
<h1>/debug/pprof/</h1>
<ul>
{{range .}}<li>{{.Count}} <a href="{{.Name}}?debug=1">{{.Name}}</a></li>
{{end}}</ul>
<p><a href="profile?seconds=30">profile</a> · <a href="trace?seconds=1">trace</a> ·
<a href="cmdline">cmdline</a> · <a href="symbol">symbol</a></p>
`))

// handleNamedProfile отдаёт профиль из [pprof.Lookup] (goroutine, heap, allocs, mutex, block, threadcreate).
// debug=0 — бинарный pprof, debug=1 и 2 — текст; gc=1 запускает сборку мусора перед heap.
func handleNamedProfile(w http.ResponseWriter, r *http.Request) {
	p := pprof.Lookup(r.PathValue("name"))
	if p == nil {
		http.NotFound(w, r)
		return
	}

	debug, err := parseDebug(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	if r.FormValue("gc") != "" && p.Name() == "heap" {
		runtime.GC()
	}

	if debug > 0 {
		setHeaders(w, "text/plain; charset=utf-8")
	} else {
		setHeaders(w, "application/octet-stream")
		w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, p.Name()))
	}

	// Заголовки и часть тела могут уйти до ошибки записи, статус уже не поменять.
	_ = p.WriteTo(w, debug)
}

func parseDebug(r *http.Request) (int, error) {
	v := r.FormValue("debug")
	if v == "" {
		return 0, nil
	}

	debug, err := strconv.Atoi(v)
	if err != nil || debug < 0 {
		return 0, fmt.Errorf("invalid debug: %q", v)
	}

	return debug, nil
}

func handleCmdline(w http.ResponseWriter, _ *http.Request) {
	setHeaders(w, "text/plain; charset=utf-8")
	//nolint:gosec // G705: ответ text/plain с nosniff, браузер его не исполняет
	_, _ = io.WriteString(w, strings.Join(os.Args, "\x00"))
}

// handleCPUProfile снимает CPU-профиль за seconds секунд.
func handleCPUProfile(w http.ResponseWriter, r *http.Request) {
	seconds := secondsParam(r, defaultCPUSeconds)
	if exceedsWriteTimeout(r, seconds) {
		http.Error(w, "profile duration exceeds server's WriteTimeout", http.StatusBadRequest)
		return
	}

	setHeaders(w, "application/octet-stream")
	w.Header().Set("Content-Disposition", `attachment; filename="profile"`)

	if err := pprof.StartCPUProfile(w); err != nil {
		// Профиль CPU один на процесс: чаще всего он уже снимается другим запросом.
		w.Header().Del("Content-Disposition")
		http.Error(w, "could not enable CPU profiling: "+err.Error(), http.StatusInternalServerError)
		return
	}
	defer pprof.StopCPUProfile()

	sleep(r.Context(), time.Duration(seconds)*time.Second)
}

// handleTrace снимает трассировку выполнения за seconds секунд.
func handleTrace(w http.ResponseWriter, r *http.Request) {
	seconds := secondsParam(r, defaultTraceSeconds)
	if exceedsWriteTimeout(r, seconds) {
		http.Error(w, "trace duration exceeds server's WriteTimeout", http.StatusBadRequest)
		return
	}

	setHeaders(w, "application/octet-stream")
	w.Header().Set("Content-Disposition", `attachment; filename="trace"`)

	if err := trace.Start(w); err != nil {
		w.Header().Del("Content-Disposition")
		http.Error(w, "could not enable tracing: "+err.Error(), http.StatusInternalServerError)
		return
	}
	defer trace.Stop()

	sleep(r.Context(), time.Duration(seconds)*time.Second)
}

// handleSymbol: GET сообщает, что таблица символов есть; POST переводит адреса «0x1+0x2» в имена функций.
func handleSymbol(w http.ResponseWriter, r *http.Request) {
	setHeaders(w, "text/plain; charset=utf-8")

	if r.Method == http.MethodGet {
		_, _ = io.WriteString(w, "num_symbols: 1\n")
		return
	}

	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxSymbolBody))
	if err != nil {
		http.Error(w, "read body: "+err.Error(), http.StatusBadRequest)
		return
	}

	var buf bytes.Buffer
	for field := range strings.SplitSeq(string(body), "+") {
		pc, perr := strconv.ParseUint(strings.TrimSpace(field), 0, 64)
		if perr != nil {
			continue
		}
		if fn := runtime.FuncForPC(uintptr(pc)); fn != nil {
			fmt.Fprintf(&buf, "%#x %s\n", pc, fn.Name())
		}
	}
	_, _ = w.Write(buf.Bytes())
}

// secondsParam читает seconds; пустое, нечисловое и неположительное значение заменяется на def.
func secondsParam(r *http.Request, def int) int {
	sec, err := strconv.Atoi(r.FormValue("seconds"))
	if err != nil || sec <= 0 {
		return def
	}

	return sec
}

// exceedsWriteTimeout сообщает, что seconds не уложится в WriteTimeout сервера: иначе соединение
// оборвалось бы до конца профиля, а клиент получил бы пустой ответ.
func exceedsWriteTimeout(r *http.Request, seconds int) bool {
	srv, ok := r.Context().Value(http.ServerContextKey).(*http.Server)

	return ok && srv.WriteTimeout > 0 && float64(seconds) >= srv.WriteTimeout.Seconds()
}

// sleep ждёт d или отмены ctx (клиент отключился, сервер закрывается).
func sleep(ctx context.Context, d time.Duration) {
	t := time.NewTimer(d)
	defer t.Stop()

	select {
	case <-t.C:
	case <-ctx.Done():
	}
}

func setHeaders(w http.ResponseWriter, contentType string) {
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Type", contentType)
}
