// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package product

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func withFakeSteamWebAPI(t *testing.T, handler http.HandlerFunc) {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	orig := steamWebAPIURL
	steamWebAPIURL = server.URL
	t.Cleanup(func() { steamWebAPIURL = orig })
}

func TestGetFileDetailsSuccess(t *testing.T) {
	withFakeSteamWebAPI(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", r.Method)
		}
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
	details, err := client.GetFileDetails(context.Background(), []uint64{111111})
	if err != nil {
		t.Fatalf("GetFileDetails: %v", err)
	}
	d, ok := details[111111]
	if !ok {
		t.Fatalf("missing details for id 111111: %+v", details)
	}
	if d.Title != "Some Mod" || d.TimeUpdated != 1700000000 || d.FileSize != 12345 {
		t.Errorf("d = %+v", d)
	}
}

func TestGetFileDetailsEmptyInputReturnsEmptyMap(t *testing.T) {
	client := &FileDetailsClient{}
	details, err := client.GetFileDetails(context.Background(), nil)
	if err != nil {
		t.Fatalf("GetFileDetails: %v", err)
	}
	if len(details) != 0 {
		t.Errorf("details = %+v, want empty", details)
	}
}

func TestGetFileDetailsRejectsOverBatchLimit(t *testing.T) {
	ids := make([]uint64, 101)
	for i := range ids {
		ids[i] = uint64(i + 1)
	}
	client := &FileDetailsClient{}
	if _, err := client.GetFileDetails(context.Background(), ids); err == nil {
		t.Fatal("expected an error for over 100 ids")
	}
}

func TestGetFileDetailsHTTPErrorPropagates(t *testing.T) {
	withFakeSteamWebAPI(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})
	client := &FileDetailsClient{}
	if _, err := client.GetFileDetails(context.Background(), []uint64{1}); err == nil {
		t.Fatal("expected an error for a non-200 response")
	}
}

func TestGetFileDetailsInvalidJSONErrors(t *testing.T) {
	withFakeSteamWebAPI(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("not json"))
	})
	client := &FileDetailsClient{}
	if _, err := client.GetFileDetails(context.Background(), []uint64{1}); err == nil {
		t.Fatal("expected an error for invalid JSON")
	}
}

func TestGetFileDetailsUnreachableServerErrors(t *testing.T) {
	orig := steamWebAPIURL
	steamWebAPIURL = "http://127.0.0.1:1" // nothing listens here
	defer func() { steamWebAPIURL = orig }()

	client := &FileDetailsClient{}
	if _, err := client.GetFileDetails(context.Background(), []uint64{1}); err == nil {
		t.Fatal("expected an error for an unreachable server")
	}
}

func TestGetFileDetailsSendsBatchedIDs(t *testing.T) {
	var gotBody string
	withFakeSteamWebAPI(t, func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		gotBody = string(body)
		_, _ = w.Write([]byte(`{"response": {"result": 1, "resultcount": 0, "publishedfiledetails": []}}`))
	})

	client := &FileDetailsClient{}
	if _, err := client.GetFileDetails(context.Background(), []uint64{111, 222}); err != nil {
		t.Fatalf("GetFileDetails: %v", err)
	}
	if !strings.Contains(gotBody, "111") || !strings.Contains(gotBody, "222") || !strings.Contains(gotBody, "itemcount=2") {
		t.Errorf("request body = %q, want both ids and itemcount=2", gotBody)
	}
}
