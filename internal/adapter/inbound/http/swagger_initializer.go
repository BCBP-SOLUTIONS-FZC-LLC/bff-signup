package http

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// SwaggerInitializerHandler serves swagger-initializer.js with the BCBP configuration.
// The BCBP dark theme is applied by intercepting /swagger/index.css in the router
// (same approach as iam-org-membership) — no customCssUrl needed here.
func SwaggerInitializerHandler(c *gin.Context) {
	c.Header("Content-Type", "application/javascript; charset=utf-8")
	c.Header("Cache-Control", "no-cache")
	c.String(http.StatusOK, swaggerInitializerJS)
}

const swaggerInitializerJS = `
window.onload = function() {
  const ui = SwaggerUIBundle({
    url: "doc.json",
    dom_id: '#swagger-ui',
    validatorUrl: null,
    oauth2RedirectUrl: ` + "`${window.location.protocol}//${window.location.host}/swagger/oauth2-redirect.html`" + `,
    persistAuthorization: false,
    presets: [
      SwaggerUIBundle.presets.apis,
      SwaggerUIStandalonePreset
    ],
    plugins: [
      SwaggerUIBundle.plugins.DownloadUrl
    ],
    layout: "StandaloneLayout",
    docExpansion: "list",
    deepLinking: true,
    defaultModelsExpandDepth: 1,
    // Keep the tag order from swagger_info.go: session → signup → infra
    tagsSorter: function() { return 0; },
    operationsSorter: function() { return 0; }
  });

  window.ui = ui;
};
`
