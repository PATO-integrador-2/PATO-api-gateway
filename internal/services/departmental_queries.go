package services

import (
	"database/sql"
	"fmt"
	"log"
	"strings"
)

// departmentalQueries expone las hojas del anexo departamental del IPM del
// DANE (anex-PMultidimensional-Departamental-2025.xlsx) cargadas en
// ipm_statistic a nivel 'departamento'. Se consultan las tablas base
// directamente porque el esquema no admite vistas ni tablas nuevas. Los
// breakdown_type 'dept_*' no los filtra ninguna vista existente, así que
// estos datos no se mezclan con los de dominio.
var departmentalQueries = map[string]string{
	// Hoja IPM_Departamentos
	"ipm_departamentos": `
		SELECT s.period AS anio, ga.name AS departamento,
		       s.breakdown_value AS area, s.value AS ipm
		FROM ipm_statistic s
		JOIN indicator i ON i.id = s.indicator_id
		JOIN geographic_area ga ON ga.id = s.geographic_area_id
		WHERE ga.level = 'departamento' AND i.code = 'MPI'
		  AND s.breakdown_type = 'dept_area'
		ORDER BY s.period, ga.name, s.breakdown_value`,

	// Hoja IPM_Indicadores_Departamento
	"ipm_indicadores_departamento": `
		SELECT s.period AS anio, ga.name AS departamento,
		       i.name AS variable, s.breakdown_value AS area, s.value AS porcentaje
		FROM ipm_statistic s
		JOIN indicator i ON i.id = s.indicator_id
		JOIN geographic_area ga ON ga.id = s.geographic_area_id
		WHERE ga.level = 'departamento' AND i.category = 'privation_variable'
		  AND s.breakdown_type = 'dept_area'
		ORDER BY s.period, ga.name, i.name, s.breakdown_value`,

	// Hoja Incidencia_Ajustada_IPM
	"incidencia_ajustada_ipm": `
		SELECT s.period AS anio, ga.name AS departamento,
		       s.value AS incidencia_ajustada
		FROM ipm_statistic s
		JOIN indicator i ON i.id = s.indicator_id
		JOIN geographic_area ga ON ga.id = s.geographic_area_id
		WHERE ga.level = 'departamento' AND i.code = 'ADJUSTED_MPI'
		  AND s.breakdown_type = 'dept_area'
		ORDER BY s.period, ga.name`,

	// Hoja IPM_Sexo
	"ipm_sexo": `
		SELECT s.period AS anio, ga.name AS departamento,
		       s.breakdown_value AS sexo, s.value AS porcentaje
		FROM ipm_statistic s
		JOIN indicator i ON i.id = s.indicator_id
		JOIN geographic_area ga ON ga.id = s.geographic_area_id
		WHERE ga.level = 'departamento' AND i.code = 'MPI'
		  AND s.breakdown_type = 'dept_person_sex'
		ORDER BY s.period, ga.name, s.breakdown_value`,

	// Hoja Contribuciones IA_IPM
	"contribuciones_ia_ipm": `
		SELECT s.period AS anio, ga.name AS departamento,
		       i.name AS dimension, s.value AS porcentaje
		FROM ipm_statistic s
		JOIN indicator i ON i.id = s.indicator_id
		JOIN geographic_area ga ON ga.id = s.geographic_area_id
		WHERE ga.level = 'departamento' AND i.category = 'dimension'
		  AND s.breakdown_type = 'dept_contribution'
		ORDER BY s.period, ga.name, i.name`,
}

// normalizedDepartmentSQL reduce el nombre a minúsculas sin tildes ni
// signos, igual que normalizeDepartment, para que "Bogotá D.C.",
// "bogota dc" o "BOGOTA D.C" encuentren el mismo departamento.
const normalizedDepartmentSQL = `regexp_replace(lower(translate(q.departamento, 'ÁÉÍÓÚÜÑáéíóúüñ', 'AEIOUUNaeiouun')), '[^a-z0-9]', '', 'g')`

var accentReplacer = strings.NewReplacer(
	"á", "a", "é", "e", "í", "i", "ó", "o", "ú", "u", "ü", "u", "ñ", "n",
	"Á", "a", "É", "e", "Í", "i", "Ó", "o", "Ú", "u", "Ü", "u", "Ñ", "n",
)

// departmentAliases mapea formas cortas de uso común al nombre normalizado
// del departamento en la BD.
var departmentAliases = map[string]string{
	"bogota":    "bogotadc",
	"sanandres": "sanandresprovidenciaysantacatalina",
}

func normalizeDepartment(value string) string {
	lowered := strings.ToLower(accentReplacer.Replace(value))

	var builder strings.Builder
	for _, r := range lowered {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			builder.WriteRune(r)
		}
	}

	normalized := builder.String()
	if alias, ok := departmentAliases[normalized]; ok {
		return alias
	}

	return normalized
}

// GetDepartmentalData devuelve las filas de una de las hojas departamentales,
// opcionalmente filtradas por departamento (vacío = todos).
func (vs *ViewService) GetDepartmentalData(name string, department string) ([]map[string]interface{}, error) {
	query, ok := departmentalQueries[name]
	if !ok {
		return nil, fmt.Errorf("consulta departamental no permitida: %s", name)
	}

	var args []interface{}

	if department = strings.TrimSpace(department); department != "" {
		query = fmt.Sprintf(
			`SELECT * FROM (%s) q WHERE %s = $1`,
			query,
			normalizedDepartmentSQL,
		)
		args = append(args, normalizeDepartment(department))
	}

	rows, err := vs.db.Query(query, args...)
	if err != nil {
		log.Printf("Error executing query: %v", err)
		return nil, err
	}
	defer rows.Close()

	return scanRows(rows)
}

// scanRows vuelca las filas de un *sql.Rows en []map[string]interface{},
// usando los nombres de columna como llaves.
func scanRows(rows *sql.Rows) ([]map[string]interface{}, error) {
	columns, err := rows.Columns()
	if err != nil {
		log.Printf("Error getting columns: %v", err)
		return nil, err
	}

	var results []map[string]interface{}

	for rows.Next() {
		rowData := make([]interface{}, len(columns))
		rowPointers := make([]interface{}, len(columns))

		for i := range rowData {
			rowPointers[i] = &rowData[i]
		}

		if err := rows.Scan(rowPointers...); err != nil {
			log.Printf("Error scanning row: %v", err)
			return nil, err
		}

		rowMap := make(map[string]interface{})

		for i, col := range columns {
			rowMap[col] = rowData[i]
		}

		results = append(results, rowMap)
	}

	if err := rows.Err(); err != nil {
		log.Printf("Error with rows: %v", err)
		return nil, err
	}

	return results, nil
}
