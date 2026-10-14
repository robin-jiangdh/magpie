// Package otel is Settings' Observability: each request's traces and
// metrics sent to an OpenTelemetry collector, such as Langfuse. While it
// sends, it stays on.
package otel

import (
	"github.com/robin-jiangdh/magpie/internal/pluginrt"
	"github.com/robin-jiangdh/magpie/internal/settings"
)

func init() {
	pluginrt.Register(pluginrt.Manifest{
		ID: "otel", API: "^1", Requires: []string{"window", "settings"},
		Name: "Observability", Sub: "Send each request's traces and metrics to an OpenTelemetry collector, such as Langfuse",
		One: "Sending to a collector", Many: "Sending to a collector",
		InUse: func() (int, error) {
			s, err := settings.LoadChecked()
			if err != nil {
				return 0, err
			}
			if s.OTel.Enabled {
				return 1, nil
			}
			return 0, nil
		},
		Parts: []pluginrt.Part{{Slot: "settings.section", Key: "otel"}},
	})
}
