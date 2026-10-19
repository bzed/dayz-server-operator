// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package product

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func withFakeSteamWebAPI(t testing.TB, handler http.HandlerFunc) {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	orig := steamWebAPIURL
	steamWebAPIURL = server.URL
	t.Cleanup(func() { steamWebAPIURL = orig })
}

func TestGetFileDetailsSuccess(t *testing.T) {
	withFakeSteamWebAPI(t, func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodPost, r.Method)
		_, _ = w.Write([]byte(`{
			"response": {
				"result": 1,
				"resultcount": 1,
				"publishedfiledetails": [
					{"publishedfileid": "111111", "result": 1, "title": "Some Mod", "file_size": "12345", "time_updated": 1700000000}
				]
			}
		}`))
	})

	client := &FileDetailsClient{}
	details, err := client.GetFileDetails(t.Context(), []uint64{111111})
	require.NoError(t, err, "GetFileDetails")
	d, ok := details[111111]
	require.True(t, ok, "missing details for id 111111: %+v", details)
	require.Equal(t, "Some Mod", d.Title)
	require.Equal(t, int64(1700000000), d.TimeUpdated)
	assert.Equal(t, int64(12345), d.FileSize)
}

func TestGetFileDetailsEmptyInputReturnsEmptyMap(t *testing.T) {
	client := &FileDetailsClient{}
	details, err := client.GetFileDetails(t.Context(), nil)
	require.NoError(t, err, "GetFileDetails")
	assert.Empty(t, details)
}

func TestGetFileDetailsRejectsOverBatchLimit(t *testing.T) {
	ids := make([]uint64, 101)
	for i := range ids {
		ids[i] = uint64(i + 1)
	}
	client := &FileDetailsClient{}
	_, err := client.GetFileDetails(t.Context(), ids)
	require.Error(t, err)
}

func TestGetFileDetailsHTTPErrorPropagates(t *testing.T) {
	withFakeSteamWebAPI(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})
	client := &FileDetailsClient{}
	_, err := client.GetFileDetails(t.Context(), []uint64{1})
	require.Error(t, err)
}

func TestGetFileDetailsInvalidJSONErrors(t *testing.T) {
	withFakeSteamWebAPI(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("not json"))
	})
	client := &FileDetailsClient{}
	_, err := client.GetFileDetails(t.Context(), []uint64{1})
	require.Error(t, err)
}

func TestGetFileDetailsUnreachableServerErrors(t *testing.T) {
	orig := steamWebAPIURL
	steamWebAPIURL = "http://127.0.0.1:1" // nothing listens here
	defer func() { steamWebAPIURL = orig }()

	client := &FileDetailsClient{}
	_, err := client.GetFileDetails(t.Context(), []uint64{1})
	require.Error(t, err)
}

func TestGetFileDetailsSendsBatchedIDs(t *testing.T) {
	var gotBody string
	withFakeSteamWebAPI(t, func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		gotBody = string(body)
		_, _ = w.Write([]byte(`{"response": {"result": 1, "resultcount": 0, "publishedfiledetails": []}}`))
	})

	client := &FileDetailsClient{}
	_, err := client.GetFileDetails(t.Context(), []uint64{111, 222})
	require.NoError(t, err, "GetFileDetails")
	require.Contains(t, gotBody, "111")
	require.Contains(t, gotBody, "222")
	assert.Contains(t, gotBody, "itemcount=2")
}
