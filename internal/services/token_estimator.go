package services

import "unicode/utf8"

// TokenEstimator estima los tokens que consumió una consulta al RAG.
//
// El RAG (Ollama + Qwen) no reporta tokens en su respuesta, así que el gateway
// los aproxima con lo que sí ve: la pregunta, la respuesta y chunks_used. El
// prompt real incluye además fragmentos de documentos e historial que el
// gateway no ve, por eso se suman constantes configurables. Calibrar estos
// valores con prompt_eval_count y eval_count medidos directamente en Ollama.
type TokenEstimator struct {
	CharsPerToken        int
	PromptOverheadTokens int
	TokensPerChunk       int
	HistoryTokens        int
}

// Text estima los tokens de un texto (redondeando hacia arriba).
func (e TokenEstimator) Text(s string) int64 {
	per := e.CharsPerToken
	if per <= 0 {
		per = 4
	}
	chars := utf8.RuneCountInString(s)
	return int64((chars + per - 1) / per)
}

// Ask estima el consumo total de una consulta: entrada + salida.
func (e TokenEstimator) Ask(question, answer string, chunksUsed int, hasHistory bool) int64 {
	if chunksUsed < 0 {
		chunksUsed = 0
	}

	input := e.Text(question)
	if chunksUsed > 0 {
		// Solo se llama a Qwen si hubo fragmentos recuperados.
		input += int64(e.PromptOverheadTokens) + int64(chunksUsed)*int64(e.TokensPerChunk)
		if hasHistory {
			input += int64(e.HistoryTokens)
		}
	}

	return input + e.Text(answer)
}
