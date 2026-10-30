package chromium

import (
	// Chromium variant: browser and Markdown engines, no LibreOffice.
	_ "github.com/gotenberg/gotenberg/v8/pkg/modules/api"
	_ "github.com/gotenberg/gotenberg/v8/pkg/modules/chromium"
	_ "github.com/gotenberg/gotenberg/v8/pkg/modules/htmlmarkdown"
	_ "github.com/gotenberg/gotenberg/v8/pkg/modules/htmlmarkdown/urlroute"
	_ "github.com/gotenberg/gotenberg/v8/pkg/modules/markdown"
	_ "github.com/gotenberg/gotenberg/v8/pkg/modules/pdfmarkdown"
	_ "github.com/gotenberg/gotenberg/v8/pkg/modules/prometheus"
	_ "github.com/gotenberg/gotenberg/v8/pkg/modules/webhook"
)
