package telemetry

import "errors"

// Config задаёт параметры инициализации OpenTelemetry.
type Config struct {
	// Enabled включает экспорт телеметрии. При false [New] ничего не создаёт
	// и не регистрирует глобальных провайдеров.
	Enabled bool
	// ServiceName попадает в атрибут ресурса service.name.
	ServiceName string
	// Endpoint — адрес OTLP gRPC Collector'а, например "localhost:4317".
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
	}
	return errors.Join(errs...)
}
