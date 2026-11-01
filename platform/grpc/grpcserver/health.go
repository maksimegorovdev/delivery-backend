package grpcserver

import (
	"context"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/status"
)

// healthServer — стандартный [health.Server], у которого Watch завершается при остановке сервера.
//
// [grpc.Server.GracefulStop] ждёт окончания всех RPC, а Watch из grpc-go живёт, пока клиент не
// отменит стрим. Без обёртки любой подписчик (client-side health checking, Watch-зонды) держал
// бы остановку до shutdownTimeout.
type healthServer struct {
	*health.Server

	stopping     context.Context //nolint:containedctx // отменяется при остановке сервера, завершает Watch
	stopWatching context.CancelFunc
}

func newHealthServer() *healthServer {
	stopping, stopWatching := context.WithCancel(context.Background())

	return &healthServer{
		Server:       health.NewServer(),
		stopping:     stopping,
		stopWatching: stopWatching,
	}
}

// shutdown переводит все сервисы в NOT_SERVING и завершает открытые Watch-стримы.
// Повторный вызов безопасен.
func (h *healthServer) shutdown() {
	h.Shutdown()
	h.stopWatching()
}

// Watch делегирует [health.Server.Watch]. При остановке сервера стрим получает NOT_SERVING
// (если он ещё не был отправлен) и завершается с [codes.Unavailable].
func (h *healthServer) Watch(req *healthpb.HealthCheckRequest, stream healthpb.Health_WatchServer) error {
	ctx, cancel := context.WithCancel(stream.Context())
	defer cancel()

	stopAfter := context.AfterFunc(h.stopping, cancel)
	defer stopAfter()

	ws := &watchStream{Health_WatchServer: stream, ctx: ctx, last: -1}
	err := h.Server.Watch(req, ws)

	// Стрим закрыл клиент, либо Watch завершился сам — отдаём его результат как есть.
	if stream.Context().Err() != nil || h.stopping.Err() == nil {
		return err
	}

	if ws.last != healthpb.HealthCheckResponse_NOT_SERVING {
		if sendErr := stream.Send(&healthpb.HealthCheckResponse{
			Status: healthpb.HealthCheckResponse_NOT_SERVING,
		}); sendErr != nil {
			return sendErr
		}
	}

	return status.Error(codes.Unavailable, "grpcserver: server is shutting down")
}

// watchStream подменяет контекст стрима и запоминает последний отправленный статус.
// Send и чтение last идут из одной горутины Watch, синхронизация не нужна.
type watchStream struct {
	healthpb.Health_WatchServer

	ctx  context.Context //nolint:containedctx // контекст стрима, подменённый на отменяемый при остановке
	last healthpb.HealthCheckResponse_ServingStatus
}

func (s *watchStream) Context() context.Context {
	return s.ctx
}

func (s *watchStream) Send(resp *healthpb.HealthCheckResponse) error {
	if err := s.Health_WatchServer.Send(resp); err != nil {
		return err
	}

	s.last = resp.GetStatus()

	return nil
}
