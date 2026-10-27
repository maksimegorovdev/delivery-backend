//nolint:testpackage // тест использует неэкспортируемые символы пакета
package telemetry

import (
	"context"
	"runtime/debug"
	"testing"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/sdk/resource"
	semconv "go.opentelemetry.io/otel/semconv/v1.43.0"
)

func attr(t *testing.T, res *resource.Resource, key string) string {
	t.Helper()
	v, ok := res.Set().Value(attribute.Key(key))
	if !ok {
		t.Fatalf("атрибут %q отсутствует в Resource: %v", key, res.Attributes())
	}
	return v.AsString()
}

func TestNewResource_ServiceAttributes(t *testing.T) {
	t.Parallel()

	res, err := newResource(context.Background(), "order")
	if err != nil {
		t.Fatalf("newResource: %v", err)
	}

	if got := attr(t, res, "service.name"); got != "order" {
		t.Errorf("service.name = %q, want %q", got, "order")
	}
	if got := attr(t, res, "service.version"); got == "" {
		t.Error("service.version пуст")
	}
	if got := attr(t, res, "service.instance.id"); got == "" {
		t.Error("service.instance.id пуст")
	}
	if got := res.SchemaURL(); got != semconv.SchemaURL {
		t.Errorf("SchemaURL = %q, want %q", got, semconv.SchemaURL)
	}
}

func TestNewResource_FromEnv(t *testing.T) {
	tests := []struct {
		name string
		env  string
		key  string
		want string
	}{
		{
			name: "атрибут из OTEL_RESOURCE_ATTRIBUTES",
			env:  "deployment.environment.name=staging,team=delivery",
			key:  "team",
			want: "delivery",
		},
		{name: "service.name из конфига главнее env", env: "service.name=other", key: "service.name", want: "order"},
		{
			name: "service.instance.id из env главнее сгенерированного",
			env:  "service.instance.id=pod-1",
			key:  "service.instance.id",
			want: "pod-1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("OTEL_RESOURCE_ATTRIBUTES", tt.env)

			res, err := newResource(context.Background(), "order")
			if err != nil {
				t.Fatalf("newResource: %v", err)
			}
			if got := attr(t, res, tt.key); got != tt.want {
				t.Errorf("%s = %q, want %q", tt.key, got, tt.want)
			}
		})
	}
}

func TestServiceVersion(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		info *debug.BuildInfo
		want string
	}{
		{name: "нет build info", info: nil, want: "unknown"},
		{name: "нет vcs.revision", info: &debug.BuildInfo{}, want: "unknown"},
		{
			name: "vcs.revision из настроек сборки",
			info: &debug.BuildInfo{Settings: []debug.BuildSetting{
				{Key: "vcs", Value: "git"},
				{Key: "vcs.revision", Value: "ced9d3b"},
			}},
			want: "ced9d3b",
		},
		{
			name: "пустой vcs.revision",
			info: &debug.BuildInfo{Settings: []debug.BuildSetting{{Key: "vcs.revision", Value: ""}}},
			want: "unknown",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := serviceVersion(tt.info); got != tt.want {
				t.Errorf("serviceVersion() = %q, want %q", got, tt.want)
			}
		})
	}
}
