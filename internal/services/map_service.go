package services

import (
	"bytes"
	"compress/gzip"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"
)

const (
	// La versión de los datos (last_loaded_at) sale de una consulta barata y se
	// cachea poco; el payload se cachea por versión, así que sólo se recalcula
	// cuando hay una carga nueva. El TTL corto evita que versiones viejas
	// queden ocupando memoria en Valkey.
	mapMetaCacheTTL = 60 * time.Second
	mapDataCacheTTL = 2 * time.Hour
)

type mapMeta struct {
	LatestYear   *int64     `json:"latest_year"`
	LastLoadedAt *time.Time `json:"last_loaded_at"`
}

// version identifica la carga actual de datos y sirve de clave y de ETag.
func (m mapMeta) version() string {
	if m.LastLoadedAt == nil {
		return "0"
	}
	return fmt.Sprintf("%d", m.LastLoadedAt.UnixNano())
}

// GetMapMetrics devuelve, ya comprimido en gzip, el JSON que colorea el mapa
// (metadatos + países por año + departamentos por año) y la versión de los
// datos para usarla como ETag. Sólo se guarda la copia comprimida.
func (vs *ViewService) GetMapMetrics() (gz []byte, version string, err error) {
	rawMeta, err := vs.cachedTTL("map:meta", mapMetaCacheTTL, func(ctx context.Context) ([]byte, error) {
		// Sólo se leen estas dos columnas: row_count obligaría a recalcular
		// las vistas de países y departamentos.
		var year sql.NullInt64
		var loaded sql.NullTime
		if err := vs.db.QueryRowContext(ctx,
			`SELECT latest_year, last_loaded_at FROM mv_map_dataset_meta`,
		).Scan(&year, &loaded); err != nil {
			return nil, err
		}

		var m mapMeta
		if year.Valid {
			m.LatestYear = &year.Int64
		}
		if loaded.Valid {
			m.LastLoadedAt = &loaded.Time
		}
		return json.Marshal(m)
	})
	if err != nil {
		return nil, "", err
	}

	var meta mapMeta
	if err := json.Unmarshal(rawMeta, &meta); err != nil {
		return nil, "", err
	}
	version = meta.version()

	gz, err = vs.cachedTTL("map:metrics:"+version, mapDataCacheTTL, func(ctx context.Context) ([]byte, error) {
		var buf bytes.Buffer
		zw := gzip.NewWriter(&buf)

		zw.Write([]byte(`{"meta":`))
		zw.Write(rawMeta)

		zw.Write([]byte(`,"countries":`))
		countries, err := vs.queryJSON(ctx,
			`SELECT * FROM mv_map_country_year ORDER BY "year", country_id, area`)
		if err != nil {
			return nil, err
		}
		zw.Write(countries)

		zw.Write([]byte(`,"departments":`))
		departments, err := vs.queryJSON(ctx,
			`SELECT * FROM mv_map_department_year ORDER BY "year", department_code`)
		if err != nil {
			return nil, err
		}
		zw.Write(departments)

		zw.Write([]byte(`}`))
		if err := zw.Close(); err != nil {
			return nil, err
		}
		return buf.Bytes(), nil
	})
	if err != nil {
		return nil, "", err
	}
	return gz, version, nil
}
