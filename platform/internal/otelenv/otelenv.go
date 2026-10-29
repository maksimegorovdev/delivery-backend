// Package otelenv изолирует тесты от переменных OTEL_* в окружении процесса.
package otelenv

import (
	"os"
	"strings"
)

// Unset удаляет OTEL_* из окружения процесса. SDK и contrib-инструментации читают их
// сами (сэмплер, лимиты атрибутов, OTEL_SEMCONV_STABILITY_OPT_IN), и значения из shell
// или CI меняли бы результат тестов. Вызывается в TestMain до m.Run; тесты, которым
// нужна переменная, задают её через t.Setenv.
//
// Unsetenv возвращает ошибку только для пустого ключа, а префикс OTEL_ его исключает,
// поэтому ветка panic недостижима и исключена из покрытия.
func Unset() {
	for _, kv := range os.Environ() {
		if key, _, _ := strings.Cut(kv, "="); strings.HasPrefix(key, "OTEL_") {
			if err := os.Unsetenv(key); err != nil { // coverage-ignore: ключ непустой
				panic(err)
			}
		}
	}
}
