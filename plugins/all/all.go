// Package all registers every built-in plugin. Only magpie's main imports
// it: core never imports a plugin, and a plugin never imports another.
package all

import (
	_ "github.com/robin-jiangdh/magpie/plugins/gateway"
	_ "github.com/robin-jiangdh/magpie/plugins/groups"
	_ "github.com/robin-jiangdh/magpie/plugins/library"
	_ "github.com/robin-jiangdh/magpie/plugins/otel"
	_ "github.com/robin-jiangdh/magpie/plugins/sessions"
	_ "github.com/robin-jiangdh/magpie/plugins/warmups"
)
