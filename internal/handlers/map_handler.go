package handlers

import (
	"bytes"
	"compress/gzip"
	"log"
	"net/http"
	"strings"

	"backend/internal/services"

	"github.com/gin-gonic/gin"
)

// MapMetricsHandler devuelve las métricas que colorean el mapa en una sola
// respuesta. Usa ETag (versión de la carga de datos) para responder 304. El
// servicio entrega el JSON ya comprimido: se envía tal cual si el cliente
// acepta gzip y sólo se descomprime en el caso raro de que no.
func MapMetricsHandler(viewService *services.ViewService) gin.HandlerFunc {
	return func(c *gin.Context) {
		gz, version, err := viewService.GetMapMetrics()
		if err != nil {
			log.Printf("map-metrics: %v", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to retrieve data"})
			return
		}

		etag := `"map-` + version + `"`
		c.Header("ETag", etag)
		c.Header("Cache-Control", "private, max-age=60, must-revalidate")
		c.Header("Vary", "Accept-Encoding")

		if c.GetHeader("If-None-Match") == etag {
			c.Status(http.StatusNotModified)
			return
		}

		const contentType = "application/json; charset=utf-8"

		if strings.Contains(c.GetHeader("Accept-Encoding"), "gzip") {
			c.Header("Content-Encoding", "gzip")
			c.Data(http.StatusOK, contentType, gz)
			return
		}

		zr, err := gzip.NewReader(bytes.NewReader(gz))
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to retrieve data"})
			return
		}
		defer zr.Close()
		c.DataFromReader(http.StatusOK, -1, contentType, zr, nil)
	}
}
