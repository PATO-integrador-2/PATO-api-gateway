package services

import "context"

// householdPovertySQL resume vw_household_poverty_by_department en una fila
// por (año, región, departamento), con los mismos nombres de columna que la
// vista. La vista pivotea una fila por hogar (708 mil filas, ~476 MB de JSON,
// ~68 s al unir 10,6 millones de privaciones); aquí se agrega en SQL sobre
// las tablas base (household_record / household_deprivation) y la respuesta
// pesa unos pocos KB.
//
// Valores: personas_que_habitan_ese_hogar es el promedio de personas por
// hogar; privacion_* es la proporción de hogares (0 a 1) con esa privación;
// ipm es el promedio del IPM de los hogares y pobre la proporción de hogares
// pobres. Los nombres de región y departamento replican los de la vista.
const householdPovertySQL = `
	WITH dep AS (
		SELECT hr.period, hr.geographic_area_id, i.code,
		       avg(hd.has_deprivation::int) AS v
		FROM household_deprivation hd
		JOIN household_record hr ON hr.id = hd.household_record_id
		JOIN indicator i ON i.id = hd.indicator_id
		GROUP BY hr.period, hr.geographic_area_id, i.code
	), piv AS (
		SELECT period, geographic_area_id,
		       max(v) FILTER (WHERE code = 'BAJO_LOGRO_EDUCATIVO') AS privacion_bajo_logro_educativo,
		       max(v) FILTER (WHERE code = 'ANALFABETISMO') AS privacion_analfabetismo,
		       max(v) FILTER (WHERE code = 'INASISTENCIA_ESCOLAR') AS privacion_inasistencia_escolar,
		       max(v) FILTER (WHERE code = 'REZAGO_ESCOLAR') AS privacion_rezago_escolar,
		       max(v) FILTER (WHERE code = 'BARRERAS_A_SERVICIOS_PARA_CUIDADO_DE_LA_PRIMERA_IN') AS privacion_atencion_integral_primera_infancia,
		       max(v) FILTER (WHERE code = 'TRABAJO_INFANTIL') AS privacion_trabajo_infantil,
		       max(v) FILTER (WHERE code = 'SIN_ASEGURAMIENTO_EN_SALUD') AS privacion_no_aseguramiento_salud,
		       max(v) FILTER (WHERE code = 'BARRERAS_DE_ACCESO_A_SERVICIOS_DE_SALUD') AS privacion_barreras_acceso_salud,
		       max(v) FILTER (WHERE code = 'DESEMPLEO_DE_LARGA_DURACION') AS privacion_desempleo_larga_duracion,
		       max(v) FILTER (WHERE code = 'TRABAJO_INFORMAL') AS privacion_tasa_empleo_formal,
		       max(v) FILTER (WHERE code = 'SIN_ACCESO_A_FUENTE_DE_AGUA_MEJORADA') AS privacion_no_acceso_agua_mejorada,
		       max(v) FILTER (WHERE code = 'INADECUADA_ELIMINACION_DE_EXCRETAS') AS privacion_inadecuada_eliminacion_excretas,
		       max(v) FILTER (WHERE code = 'MATERIAL_INADECUADO_DE_PISOS') AS privacion_inadecuado_material_pisos,
		       max(v) FILTER (WHERE code = 'MATERIAL_INADECUADO_DE_PAREDES_EXTERIORES') AS privacion_inadecuado_material_paredes,
		       max(v) FILTER (WHERE code = 'HACINAMIENTO_CRITICO') AS privacion_hacinamiento_critico
		FROM dep
		GROUP BY period, geographic_area_id
	), b AS (
		SELECT period, geographic_area_id,
		       round(avg(household_size)::numeric, 2) AS personas,
		       round(avg(ipm_value)::numeric, 4) AS ipm,
		       round(avg(is_poor::int)::numeric, 4) AS pobre
		FROM household_record
		GROUP BY period, geographic_area_id
	)
	SELECT b.period AS anio,
	       CASE
            WHEN b.period <= 2025 THEN
            CASE ga_region.official_code::integer
                WHEN 1 THEN 'Caribe'::text
                WHEN 2 THEN 'Oriental'::text
                WHEN 3 THEN 'Central'::text
                WHEN 4 THEN 'Pacífica'::text
                WHEN 5 THEN 'Orinoquía - Amazonía'::text
                WHEN 6 THEN 'Bogotá D.C.'::text
                ELSE 'Sin clasificar'::text
            END
            ELSE
            CASE ga_region.official_code::integer
                WHEN 1 THEN 'Caribe'::text
                WHEN 2 THEN 'Oriental'::text
                WHEN 3 THEN 'Central'::text
                WHEN 4 THEN 'Pacífica (sin Valle)'::text
                WHEN 5 THEN 'Bogotá'::text
                WHEN 6 THEN 'Antioquia'::text
                WHEN 7 THEN 'Valle del Cauca'::text
                WHEN 8 THEN 'San Andrés y Providencia'::text
                WHEN 9 THEN 'Orinoquía - Amazonía'::text
                ELSE 'Sin clasificar'::text
            END
        END AS region,
	       CASE ga_dept.official_code::integer
            WHEN 5 THEN 'Antioquia'::text
            WHEN 8 THEN 'Atlántico'::text
            WHEN 11 THEN 'Bogotá, D.C.'::text
            WHEN 13 THEN 'Bolívar'::text
            WHEN 15 THEN 'Boyacá'::text
            WHEN 17 THEN 'Caldas'::text
            WHEN 18 THEN 'Caquetá'::text
            WHEN 19 THEN 'Cauca'::text
            WHEN 20 THEN 'Cesar'::text
            WHEN 23 THEN 'Córdoba'::text
            WHEN 25 THEN 'Cundinamarca'::text
            WHEN 27 THEN 'Chocó'::text
            WHEN 41 THEN 'Huila'::text
            WHEN 44 THEN 'La Guajira'::text
            WHEN 47 THEN 'Magdalena'::text
            WHEN 50 THEN 'Meta'::text
            WHEN 52 THEN 'Nariño'::text
            WHEN 54 THEN 'Norte de Santander'::text
            WHEN 63 THEN 'Quindío'::text
            WHEN 66 THEN 'Risaralda'::text
            WHEN 68 THEN 'Santander'::text
            WHEN 70 THEN 'Sucre'::text
            WHEN 73 THEN 'Tolima'::text
            WHEN 76 THEN 'Valle del Cauca'::text
            WHEN 81 THEN 'Arauca'::text
            WHEN 85 THEN 'Casanare'::text
            WHEN 86 THEN 'Putumayo'::text
            WHEN 88 THEN 'Archipiélago de San Andrés, Providencia y Santa Catalina'::text
            WHEN 91 THEN 'Amazonas'::text
            WHEN 94 THEN 'Guainía'::text
            WHEN 95 THEN 'Guaviare'::text
            WHEN 97 THEN 'Vaupés'::text
            WHEN 99 THEN 'Vichada'::text
            ELSE 'Sin clasificar'::text
        END AS departamento,
	       b.personas AS personas_que_habitan_ese_hogar,
	       round(p.privacion_bajo_logro_educativo::numeric, 4) AS privacion_bajo_logro_educativo,
	       round(p.privacion_analfabetismo::numeric, 4) AS privacion_analfabetismo,
	       round(p.privacion_inasistencia_escolar::numeric, 4) AS privacion_inasistencia_escolar,
	       round(p.privacion_rezago_escolar::numeric, 4) AS privacion_rezago_escolar,
	       round(p.privacion_atencion_integral_primera_infancia::numeric, 4) AS privacion_atencion_integral_primera_infancia,
	       round(p.privacion_trabajo_infantil::numeric, 4) AS privacion_trabajo_infantil,
	       round(p.privacion_no_aseguramiento_salud::numeric, 4) AS privacion_no_aseguramiento_salud,
	       round(p.privacion_barreras_acceso_salud::numeric, 4) AS privacion_barreras_acceso_salud,
	       round(p.privacion_desempleo_larga_duracion::numeric, 4) AS privacion_desempleo_larga_duracion,
	       round(p.privacion_tasa_empleo_formal::numeric, 4) AS privacion_tasa_empleo_formal,
	       round(p.privacion_no_acceso_agua_mejorada::numeric, 4) AS privacion_no_acceso_agua_mejorada,
	       round(p.privacion_inadecuada_eliminacion_excretas::numeric, 4) AS privacion_inadecuada_eliminacion_excretas,
	       round(p.privacion_inadecuado_material_pisos::numeric, 4) AS privacion_inadecuado_material_pisos,
	       round(p.privacion_inadecuado_material_paredes::numeric, 4) AS privacion_inadecuado_material_paredes,
	       round(p.privacion_hacinamiento_critico::numeric, 4) AS privacion_hacinamiento_critico,
	       b.ipm AS ipm,
	       b.pobre AS pobre
	FROM b
	JOIN piv p ON p.period = b.period AND p.geographic_area_id = b.geographic_area_id
	JOIN geographic_area ga_dept ON ga_dept.id = b.geographic_area_id
	LEFT JOIN geographic_area ga_region ON ga_region.id = ga_dept.parent_id
	ORDER BY anio, departamento`

// GetHouseholdPovertyByDepartment devuelve el resumen por año y departamento.
func (vs *ViewService) GetHouseholdPovertyByDepartment() ([]byte, error) {
	return vs.cached("view:household_poverty_summary", func(ctx context.Context) ([]byte, error) {
		return vs.queryJSON(ctx, householdPovertySQL)
	})
}
