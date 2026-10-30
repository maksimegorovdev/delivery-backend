//nolint:testpackage // тест использует неэкспортируемые символы пакета
package telemetry

import (
	"context"
	"runtime/debug"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/sdk/resource"
	semconv "go.opentelemetry.io/otel/semconv/v1.43.0"
)

func attr(t *testing.T, res *resource.Resource, key string) string {
	t.Helper()
	v, ok := res.Set().Value(attribute.Key(key))
	require.True(t, ok, "attribute %q is missing in Resource: %v", key, res.Attributes())
	return v.AsString()
}

func TestNewResource_ServiceAttributes(t *testing.T) {
	t.Parallel()

	res, err := newResource(context.Background(), "order")
	require.NoError(t, err)

	assert.Equal(t, "order", attr(t, res, "service.name"))
	assert.NotEmpty(t, attr(t, res, "service.version"))
	assert.NotEmpty(t, attr(t, res, "service.instance.id"))
	assert.Equal(t, semconv.SchemaURL, res.SchemaURL())
}

func TestNewResource_FromEnv(t *testing.T) {
	tests := []struct {
		name string
		env  string
		key  string
		want string
	}{
		{
			name: "attribute from OTEL_RESOURCE_ATTRIBUTES",
			env:  "deployment.environment.name=staging,team=delivery",
			key:  "team",
			want: "delivery",
		},
		{name: "service.name from config wins over env", env: "service.name=other", key: "service.name", want: "order"},
		{
			name: "service.instance.id from env wins over generated",
			env:  "service.instance.id=pod-1",
			key:  "service.instance.id",
			want: "pod-1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("OTEL_RESOURCE_ATTRIBUTES", tt.env)

			res, err := newResource(context.Background(), "order")
			require.NoError(t, err)
			assert.Equal(t, tt.want, attr(t, res, tt.key))
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
		{name: "no build info", info: nil, want: "unknown"},
		{name: "no vcs.revision", info: &debug.BuildInfo{}, want: "unknown"},
		{
			name: "vcs.revision from build settings",
			info: &debug.BuildInfo{Settings: []debug.BuildSetting{
				{Key: "vcs", Value: "git"},
				{Key: "vcs.revision", Value: "ced9d3b"},
			}},
			want: "ced9d3b",
		},
		{
			name: "empty vcs.revision",
			info: &debug.BuildInfo{Settings: []debug.BuildSetting{{Key: "vcs.revision", Value: ""}}},
			want: "unknown",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tt.want, serviceVersion(tt.info))
		})
	}
}
