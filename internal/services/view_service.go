package services

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
	"golang.org/x/sync/singleflight"
)

const (
	// Las vistas son datos analíticos que cambian muy poco: se cachea el JSON
	// ya serializado para no volver a leer ni materializar la vista en cada request.
	viewCacheTTL  = 10 * time.Minute
	viewQueryTime = 30 * time.Second
)

type ViewService struct {
	db    *sql.DB
	cache *redis.Client
	group singleflight.Group
}

// NewViewService crea el servicio. cache puede ser nil (sin caché).
func NewViewService(db *sql.DB, cache *redis.Client) *ViewService {
	return &ViewService{db: db, cache: cache}
}

var allowedViews = map[string]bool{
	"vw_ipm_by_domain":                        true,
	"vw_average_deprivations":                 true,
	"vw_deprivations_by_variable":             true,
	"vw_dashboard03_national_poverty":         true,
	"vw_dashboard03_poverty_by_age":           true,
	"vw_dashboard03_deprivation_contribution": true,
	"vw_dimension_contribution":               true,
	"vw_incidence_by_household_head_sex":      true,
	"vw_incidence_by_person_sex":              true,
	// vw_household_poverty_by_department no va aquí: tiene una fila por hogar
	// (708 mil) y filtrarla o pedir sus categorías repite un pivote de ~68 s.
	// Se sirve resumida en GetHouseholdPovertyByDepartment.
}

var allowedColumns = map[string]bool{
	"anio":                           true,
	"dominio":                        true,
	"variable":                       true,
	"area_geografica":                true,
	"pais":                           true,
	"grupo_erario":                   true,
	"valor_porcentaje":               true,
	"privacion":                      true,
	"tipo_medida_pm":                 true,
	"dimension":                      true,
	"porcentaje":                     true,
	"region":                         true,
	"departamento":                   true,
	"sexo":                           true,
	"personas_que_habitan_ese_hogar": true,
	"privacion_bajo_logro_educativo": true,
	"privacion_analfabetismo":        true,
	"privacion_inasistencia_escolar": true,
	"privacion_rezago_escolar":       true,
	"privacion_atencion_integral_primera_infancia": true,
	"privacion_trabajo_infantil":                   true,
	"privacion_no_aseguramiento_salud":             true,
	"privacion_barreras_acceso_salud":              true,
	"privacion_desempleo_larga_duracion":           true,
	"privacion_tasa_empleo_formal":                 true,
	"privacion_no_acceso_agua_mejorada":            true,
	"privacion_inadecuada_eliminacion_excretas":    true,
	"privacion_inadecuado_material_pisos":          true,
	"privacion_inadecuado_material_paredes":        true,
	"privacion_hacinamiento_critico":               true,
	"ipm":                                          true,
	"pobre":                                        true,
}

// cached devuelve el JSON cacheado para key o lo carga con load. Las cargas
// concurrentes de la misma key se colapsan en una sola consulta (singleflight).
// Un fallo del caché nunca rompe la request: se cae a la base de datos.
func (vs *ViewService) cached(key string, load func(ctx context.Context) ([]byte, error)) ([]byte, error) {
	return vs.cachedTTL(key, viewCacheTTL, load)
}

// cachedTTL es cached con un TTL propio para la key.
func (vs *ViewService) cachedTTL(key string, ttl time.Duration, load func(ctx context.Context) ([]byte, error)) ([]byte, error) {
	ctx := context.Background()

	if vs.cache != nil {
		if b, err := vs.cache.Get(ctx, key).Bytes(); err == nil {
			return b, nil
		}
	}

	v, err, _ := vs.group.Do(key, func() (interface{}, error) {
		qctx, cancel := context.WithTimeout(ctx, viewQueryTime)
		defer cancel()

		b, err := load(qctx)
		if err != nil {
			return nil, err
		}
		if vs.cache != nil {
			if err := vs.cache.Set(ctx, key, b, ttl).Err(); err != nil {
				log.Printf("Error guardando %s en caché: %v", key, err)
			}
		}
		return b, nil
	})
	if err != nil {
		return nil, err
	}
	return v.([]byte), nil
}

// GetViewTotalData devuelve el JSON (array de objetos) de toda la vista.
func (vs *ViewService) GetViewTotalData(viewName string) ([]byte, error) {
	if !allowedViews[viewName] {
		return nil, fmt.Errorf("vista no permitida: %s", viewName)
	}

	return vs.cached("view:total:"+viewName, func(ctx context.Context) ([]byte, error) {
		query := fmt.Sprintf(`SELECT * FROM "%s" ORDER BY "anio" ASC`, viewName)
		return vs.queryJSON(ctx, query)
	})
}

// GetDistinctColumnValues devuelve los valores únicos de una columna dentro de una vista,
// usado para poblar filtros (categorías IPM, ubicación geográfica, etc.).
func (vs *ViewService) GetDistinctColumnValues(viewName string, columnName string) ([]string, error) {
	if !allowedViews[viewName] {
		return nil, fmt.Errorf("vista no permitida: %s", viewName)
	}

	if !allowedColumns[columnName] {
		return nil, fmt.Errorf("columna no permitida: %s", columnName)
	}

	raw, err := vs.cached("view:cat:"+viewName+":"+columnName, func(ctx context.Context) ([]byte, error) {
		query := fmt.Sprintf(
			`SELECT DISTINCT "%s" FROM "%s" WHERE "%s" IS NOT NULL ORDER BY "%s" ASC`,
			columnName, viewName, columnName, columnName,
		)

		rows, err := vs.db.QueryContext(ctx, query)
		if err != nil {
			log.Printf("Error executing query: %v", err)
			return nil, err
		}
		defer rows.Close()

		values := make([]string, 0)
		for rows.Next() {
			var v interface{}
			if err := rows.Scan(&v); err != nil {
				log.Printf("Error scanning row: %v", err)
				return nil, err
			}
			if v == nil {
				continue
			}
			values = append(values, fmt.Sprintf("%v", v))
		}
		if err := rows.Err(); err != nil {
			log.Printf("Error with rows: %v", err)
			return nil, err
		}
		return json.Marshal(values)
	})
	if err != nil {
		return nil, err
	}

	var values []string
	if err := json.Unmarshal(raw, &values); err != nil {
		return nil, err
	}
	return values, nil
}

func (vs *ViewService) GetViewFilteredData(
	viewName string,
	columnName string,
	columnValue string,
) ([]byte, error) {

	if !allowedViews[viewName] {
		return nil, fmt.Errorf("vista no permitida: %s", viewName)
	}

	if !allowedColumns[columnName] {
		return nil, fmt.Errorf("columna no permitida: %s", columnName)
	}

	key := fmt.Sprintf("view:filter:%s:%s=%s", viewName, columnName, columnValue)
	return vs.cached(key, func(ctx context.Context) ([]byte, error) {
		query := fmt.Sprintf(
			`SELECT * FROM "%s" WHERE "%s" = $1 ORDER BY "anio" ASC`,
			viewName, columnName,
		)
		return vs.queryJSON(ctx, query, columnValue)
	})
}

// ColumnFilter representa un par columna/valor usado para filtrar una vista.
type ColumnFilter struct {
	ColumnName  string
	ColumnValue string
}

// GetViewFilteredDataMulti filtra una vista por dos o más columnas a la vez
// (ej. dominio + pais + anio), combinando las condiciones con AND.
func (vs *ViewService) GetViewFilteredDataMulti(
	viewName string,
	filters []ColumnFilter,
) ([]byte, error) {

	if !allowedViews[viewName] {
		return nil, fmt.Errorf("vista no permitida: %s", viewName)
	}

	if len(filters) == 0 {
		return nil, fmt.Errorf("se requiere al menos un filtro")
	}

	conditions := make([]string, 0, len(filters))
	args := make([]interface{}, 0, len(filters))
	keyParts := make([]string, 0, len(filters))

	for i, f := range filters {
		if !allowedColumns[f.ColumnName] {
			return nil, fmt.Errorf("columna no permitida: %s", f.ColumnName)
		}
		conditions = append(conditions, fmt.Sprintf(`"%s" = $%d`, f.ColumnName, i+1))
		args = append(args, f.ColumnValue)
		keyParts = append(keyParts, f.ColumnName+"="+f.ColumnValue)
	}

	query := fmt.Sprintf(
		`SELECT * FROM "%s" WHERE %s ORDER BY "anio" ASC`,
		viewName,
		strings.Join(conditions, " AND "),
	)

	key := "view:multi:" + viewName + ":" + strings.Join(keyParts, "&")
	return vs.cached(key, func(ctx context.Context) ([]byte, error) {
		return vs.queryJSON(ctx, query, args...)
	})
}

// queryJSON ejecuta la consulta y serializa cada fila a JSON a medida que la
// lee, sin acumular un []map[string]interface{} con todo el resultado.
func (vs *ViewService) queryJSON(ctx context.Context, query string, args ...interface{}) ([]byte, error) {
	rows, err := vs.db.QueryContext(ctx, query, args...)
	if err != nil {
		log.Printf("Error executing query: %v", err)
		return nil, err
	}
	defer rows.Close()

	columns, err := rows.Columns()
	if err != nil {
		log.Printf("Error getting columns: %v", err)
		return nil, err
	}

	// Estructuras reutilizadas para todas las filas.
	rowData := make([]interface{}, len(columns))
	rowPointers := make([]interface{}, len(columns))
	for i := range rowData {
		rowPointers[i] = &rowData[i]
	}
	rowMap := make(map[string]interface{}, len(columns))

	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	buf.WriteByte('[')

	first := true
	for rows.Next() {
		if err := rows.Scan(rowPointers...); err != nil {
			log.Printf("Error scanning row: %v", err)
			return nil, err
		}

		for i, col := range columns {
			rowMap[col] = rowData[i]
		}

		if !first {
			buf.WriteByte(',')
		}
		first = false

		if err := enc.Encode(rowMap); err != nil {
			return nil, err
		}
		// Encode añade '\n' al final de cada valor.
		buf.Truncate(buf.Len() - 1)
	}

	if err := rows.Err(); err != nil {
		log.Printf("Error with rows: %v", err)
		return nil, err
	}

	buf.WriteByte(']')
	return buf.Bytes(), nil
}
