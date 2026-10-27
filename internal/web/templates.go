package web

import "syn/internal/web/templates"

// Re-export HTML templates from internal/web/templates for callers in package web.
const (
	BaseCSS     = templates.BaseCSS
	HomeHTML    = templates.HomeHTML
	TermsHTML   = templates.TermsHTML
	PrivacyHTML = templates.PrivacyHTML
)
