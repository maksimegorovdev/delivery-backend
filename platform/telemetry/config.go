package telemetry

import (
	"errors"
	"net/url"
)

// Config задаёт параметры инициализации OpenTelemetry.
type Config struct {
	// Enabled включает экспорт телеметрии. При false [New] ничего не создаёт
	// и не регистрирует глобальных провайдеров.
	Enabled bool
	// ServiceName попадает в атрибут ресурса service.name.
	ServiceName string
	// Endpoint — URL OTLP gRPC Collector'а в формате OTEL_EXPORTER_OTLP_ENDPOINT,
	// например "http://localhost:4317". Схема http означает соединение без TLS, https — с TLS.
	Endpoint string
}

// validate проверяет Config; для Enabled=false любые значения допустимы.
func (c Config) validate() error {
	if !c.Enabled {
		return nil
	}

	var errs []error
	if c.ServiceName == "" {
		errs = append(errs, errors.New("telemetry: service name is required"))
	}
	if c.Endpoint == "" {
		errs = append(errs, errors.New("telemetry: endpoint is required"))
	} else if !isEndpointURL(c.Endpoint) {
		errs = append(errs, errors.New(`telemetry: endpoint must be a URL like "http://host:4317"`))
	}
	return errors.Join(errs...)
}

// isEndpointURL сообщает, задан ли endpoint как URL со схемой http/https и хостом.
// Без проверки "localhost:4317" разобрался бы как схема "localhost" и экспорт
// молча не работал бы.
func isEndpointURL(endpoint string) bool {
	u, err := url.Parse(endpoint)
	if err != nil {
		return false
	}
	return (u.Scheme == "http" || u.Scheme == "https") && u.Host != ""
}
