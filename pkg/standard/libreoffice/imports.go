package libreoffice

import (
	// LibreOffice variant: Office documents to Markdown, no Chromium.
	_ "github.com/gotenberg/gotenberg/v8/pkg/modules/api"
	_ "github.com/gotenberg/gotenberg/v8/pkg/modules/htmlmarkdown"
	_ "github.com/gotenberg/gotenberg/v8/pkg/modules/libreoffice/api"
	_ "github.com/gotenberg/gotenberg/v8/pkg/modules/libreoffice/office"
	_ "github.com/gotenberg/gotenberg/v8/pkg/modules/markdown"
	_ "github.com/gotenberg/gotenberg/v8/pkg/modules/pdfmarkdown"
	_ "github.com/gotenberg/gotenberg/v8/pkg/modules/prometheus"
	_ "github.com/gotenberg/gotenberg/v8/pkg/modules/webhook"
)
