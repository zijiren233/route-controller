package controller_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/zijiren233/route-controller/internal/controller"
)

func TestStatusEndpoint(t *testing.T) {
	t.Parallel()

	store := controller.NewStatusStore(false)
	request := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/status", nil)
	recorder := httptest.NewRecorder()
	store.ServeHTTP(recorder, request)
	assert.Equal(t, http.StatusOK, recorder.Code)
	assert.Contains(t, recorder.Body.String(), `"ready":false`)
	assert.NotContains(t, recorder.Body.String(), "lastSuccessAt")

	request = httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/status", nil)
	recorder = httptest.NewRecorder()
	store.ServeHTTP(recorder, request)
	assert.Equal(t, http.StatusMethodNotAllowed, recorder.Code)
}
