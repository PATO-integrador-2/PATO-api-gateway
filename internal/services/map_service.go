package services

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"time"
)

// El payload es chico (unos 7 KB) y la vista lo calcula con agregaciones
// sobre las vistas del mapa, así que se cachea poco: así una carga nueva se
// ve en menos de un minuto.
const mapPayloadCacheTTL = 60 * time.Second

// GetMapMetrics devuelve, ya comprimido en gzip, el JSON que colorea el mapa
// tal como lo arma la vista vw_map_metrics_payload, y su versión
// (datasetVersion) para usarla como ETag.
func (vs *ViewService) GetMapMetrics() (gz []byte, version string, err error) {
	raw, err := vs.cachedTTL("map:payload", mapPayloadCacheTTL, func(ctx context.Context) ([]byte, error) {
		var payload []byte
		if err := vs.db.QueryRowContext(ctx,
			`SELECT payload FROM vw_map_metrics_payload`,
		).Scan(&payload); err != nil {
			return nil, err
		}
		return payload, nil
	})
	if err != nil {
		return nil, "", err
	}

	var meta struct {
		DatasetVersion string `json:"datasetVersion"`
	}
	if err := json.Unmarshal(raw, &meta); err != nil {
		return nil, "", err
	}
	version = meta.DatasetVersion
	if version == "" {
		version = "0"
	}

	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	if _, err := zw.Write(raw); err != nil {
		return nil, "", err
	}
	if err := zw.Close(); err != nil {
		return nil, "", err
	}
	return buf.Bytes(), version, nil
}
