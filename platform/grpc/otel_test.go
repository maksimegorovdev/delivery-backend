package grpc_test

import (
	"context"
	"io"
	"net"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/propagation"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/grpc"
	grpccodes "google.golang.org/grpc/codes"
	"google.golang.org/grpc/reflection/grpc_reflection_v1"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
	"google.golang.org/protobuf/types/known/emptypb"

	"github.com/maksimegorovdev/delivery-backend/platform/grpc/grpcclient"
	"github.com/maksimegorovdev/delivery-backend/platform/grpc/grpcserver"
)

const (
	echoService    = "test.Echo"
	callOK         = "/test.Echo/OK"
	callInternal   = "/test.Echo/Internal"
	callNotFound   = "/test.Echo/NotFound"
	bufferSize     = 1 << 20
	serverDuration = "rpc.server.call.duration"
	spanWait       = 5 * time.Second
	spanPoll       = 5 * time.Millisecond
)

//nolint:gochecknoglobals // глобальные провайдеры OTel ставятся один раз в TestMain
var (
	spans   = tracetest.NewInMemoryExporter()
	metrics = sdkmetric.NewManualReader()
)

// Глобальные провайдеры ставятся один раз: otelgrpc берёт их при создании
// хендлера через делегирующий глобальный провайдер.
func TestMain(m *testing.M) {
	otel.SetTracerProvider(sdktrace.NewTracerProvider(sdktrace.WithSyncer(spans)))
	otel.SetMeterProvider(sdkmetric.NewMeterProvider(sdkmetric.WithReader(metrics)))
	otel.SetTextMapPropagator(propagation.TraceContext{})
	os.Exit(m.Run())
}

type echoServer any

func echoDesc() *grpc.ServiceDesc {
	handler := func(fn func() error) grpc.MethodHandler {
		return func(_ any, _ context.Context, dec func(any) error, _ grpc.UnaryServerInterceptor) (any, error) {
			if err := dec(&emptypb.Empty{}); err != nil {
				return nil, err
			}
			return &emptypb.Empty{}, fn()
		}
	}
	return &grpc.ServiceDesc{
		ServiceName: echoService,
		HandlerType: (*echoServer)(nil),
		Methods: []grpc.MethodDesc{
			{MethodName: "OK", Handler: handler(func() error { return nil })},
			{
				MethodName: "Internal",
				Handler:    handler(func() error { return status.Error(grpccodes.Internal, "boom") }),
			},
			{
				MethodName: "NotFound",
				Handler:    handler(func() error { return status.Error(grpccodes.NotFound, "none") }),
			},
		},
	}
}

func newConn(t *testing.T) *grpc.ClientConn {
	t.Helper()

	lis := bufconn.Listen(bufferSize)
	srv := grpcserver.New("bufnet", grpcserver.WithReflection(true))
	srv.Server().RegisterService(echoDesc(), struct{}{})
	go func() { _ = srv.Server().Serve(lis) }()
	t.Cleanup(srv.Server().Stop)

	conn, err := grpcclient.New("passthrough:///bufnet", grpcclient.WithDialOption(
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
			return lis.DialContext(ctx)
		}),
	))
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })

	return conn
}

// rootContext открывает корневой спан, чтобы отделять спаны теста от соседних параллельных.
func rootContext(t *testing.T) (context.Context, trace.TraceID) {
	t.Helper()
	ctx, span := otel.Tracer("test").Start(context.Background(), t.Name())
	t.Cleanup(func() { span.End() })
	return ctx, span.SpanContext().TraceID()
}

func call(ctx context.Context, conn *grpc.ClientConn, method string) error {
	return conn.Invoke(ctx, method, &emptypb.Empty{}, &emptypb.Empty{})
}

func spansOfKind(traceID trace.TraceID, kind trace.SpanKind) []tracetest.SpanStub {
	var out []tracetest.SpanStub
	for _, s := range spans.GetSpans() {
		if s.SpanContext.TraceID() == traceID && s.SpanKind == kind {
			out = append(out, s)
		}
	}
	return out
}

// singleSpan ждёт ровно один спан вида kind. Ожидание нужно для серверных спанов:
// grpc-go закрывает их в defer уже после отправки ответа, и клиент возвращается раньше.
func singleSpan(t *testing.T, traceID trace.TraceID, kind trace.SpanKind) tracetest.SpanStub {
	t.Helper()
	var found []tracetest.SpanStub
	require.Eventually(t, func() bool {
		found = spansOfKind(traceID, kind)
		return len(found) >= 1
	}, spanWait, spanPoll, "spans of kind %s", kind)
	require.Len(t, found, 1, "spans of kind %s", kind)
	return found[0]
}

func TestClientSpanIsParentOfServerSpan(t *testing.T) {
	t.Parallel()
	conn := newConn(t)
	ctx, traceID := rootContext(t)

	require.NoError(t, call(ctx, conn, callOK))

	client := singleSpan(t, traceID, trace.SpanKindClient)
	server := singleSpan(t, traceID, trace.SpanKindServer)
	require.Equal(t, client.SpanContext.TraceID(), server.SpanContext.TraceID())
	require.Equal(t, client.SpanContext.SpanID(), server.Parent.SpanID())
}

func TestReflectionCallCreatesNoSpans(t *testing.T) {
	t.Parallel()
	conn := newConn(t)
	ctx, traceID := rootContext(t)

	stream, err := grpc_reflection_v1.NewServerReflectionClient(conn).ServerReflectionInfo(ctx)
	require.NoError(t, err)
	require.NoError(t, stream.Send(&grpc_reflection_v1.ServerReflectionRequest{
		MessageRequest: &grpc_reflection_v1.ServerReflectionRequest_ListServices{},
	}))
	_, err = stream.Recv()
	require.NoError(t, err)
	require.NoError(t, stream.CloseSend())
	_, err = stream.Recv()
	require.ErrorIs(t, err, io.EOF)

	// Контрольный вызов в том же трейсе: когда его серверный спан выгружен, спан
	// reflection-стрима, будь он, тоже уже закрыт — пустота проверяется не «рано».
	require.NoError(t, call(ctx, conn, callOK))
	server := singleSpan(t, traceID, trace.SpanKindServer)

	// Серверный фильтр режет reflection, клиент трассируется как есть, поэтому проверяем сервер.
	require.Equal(t, "test.Echo/OK", server.Name)
}

func TestServerSpanStatus(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		method string
		want   codes.Code
	}{
		{name: "internal is error", method: callInternal, want: codes.Error},
		{name: "not found is not a server error", method: callNotFound, want: codes.Unset},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			conn := newConn(t)
			ctx, traceID := rootContext(t)

			require.Error(t, call(ctx, conn, tt.method))

			require.Equal(t, tt.want, singleSpan(t, traceID, trace.SpanKindServer).Status.Code)
		})
	}
}

func TestServerRecordsRPCDurationMetric(t *testing.T) {
	t.Parallel()
	conn := newConn(t)

	require.NoError(t, call(context.Background(), conn, callOK))

	// Метрика пишется вместе с серверным спаном, после отправки ответа.
	var attrs map[string]string
	require.Eventually(t, func() bool {
		attrs = okDurationAttrs()
		return len(attrs) > 0
	}, spanWait, spanPoll)

	// otelgrpc v0.72 пишет семконвенцию rpc.*: сервис входит в rpc.method.
	require.Equal(t, "test.Echo/OK", attrs["rpc.method"])
	require.Equal(t, "OK", attrs["rpc.response.status_code"])
}

// okDurationAttrs собирает атрибуты точек rpc.server.call.duration вызова test.Echo/OK.
// Вызывается из условия require.Eventually в чужой горутине, поэтому без require.
func okDurationAttrs() map[string]string {
	var rm metricdata.ResourceMetrics
	if err := metrics.Collect(context.Background(), &rm); err != nil {
		return nil
	}

	attrs := map[string]string{}
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			h, ok := m.Data.(metricdata.Histogram[float64])
			if !ok || len(h.DataPoints) == 0 || m.Name != serverDuration {
				continue
			}
			for _, dp := range h.DataPoints {
				method, _ := dp.Attributes.Value("rpc.method")
				if method.AsString() != "test.Echo/OK" {
					continue
				}
				for _, kv := range dp.Attributes.ToSlice() {
					attrs[string(kv.Key)] = kv.Value.String()
				}
			}
		}
	}
	return attrs
}
