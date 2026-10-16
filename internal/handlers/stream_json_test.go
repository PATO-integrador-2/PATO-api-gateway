package handlers

import (
	"compress/gzip"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func runStream(acceptGzip bool, write func(w io.Writer) error) *httptest.ResponseRecorder {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodGet, "/x", nil)
	if acceptGzip {
		c.Request.Header.Set("Accept-Encoding", "gzip")
	}
	streamJSON(c, write)
	return rec
}

func TestStreamJSONPlain(t *testing.T) {
	rec := runStream(false, func(w io.Writer) error {
		_, err := io.WriteString(w, `[{"a":1}]`)
		return err
	})
	if rec.Code != http.StatusOK || rec.Body.String() != `[{"a":1}]` {
		t.Fatalf("got %d %q", rec.Code, rec.Body.String())
	}
}

func TestStreamJSONGzip(t *testing.T) {
	rec := runStream(true, func(w io.Writer) error {
		_, err := io.WriteString(w, `[{"a":1}]`)
		return err
	})
	if rec.Header().Get("Content-Encoding") != "gzip" {
		t.Fatal("falta Content-Encoding gzip")
	}
	zr, err := gzip.NewReader(rec.Body)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(zr)
	if string(body) != `[{"a":1}]` {
		t.Fatalf("got %q", body)
	}
}

func TestStreamJSONErrorBeforeWrite(t *testing.T) {
	rec := runStream(true, func(w io.Writer) error {
		return errors.New("timeout")
	})
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("got %d", rec.Code)
	}
	if rec.Header().Get("Content-Encoding") != "" {
		t.Fatal("el error no debe ir marcado como gzip")
	}
}
