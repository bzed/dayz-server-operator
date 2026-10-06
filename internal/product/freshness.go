// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package product

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

// FileDetails is the subset of Steam's GetPublishedFileDetails response
// the update engine needs: TimeUpdated is the freshness signal (D14: not
// local mtime), and Title/FileSize let a forced refresh sanity-check a
// download against what Steam expects.
type FileDetails struct {
	PublishedFileID uint64
	Result          int // Steam's per-item result code; 1 means success
	Title           string
	FileSize        int64
	TimeUpdated     int64 // Unix seconds
}

// steamWebAPIURL is the GetPublishedFileDetails endpoint (no API key
// required for this specific method, per Steamworks documentation); a var
// so tests can point it at an httptest server.
var steamWebAPIURL = "https://api.steampowered.com/ISteamRemoteStorage/GetPublishedFileDetails/v1/"

// FileDetailsClient looks up workshop item metadata from the Steam Web
// API, batched per call as the endpoint requires.
type FileDetailsClient struct {
	HTTPClient *http.Client // defaults to http.DefaultClient
}

// GetFileDetails fetches metadata for the given published file (workshop
// item) ids in one batched request. Steam limits a single call to 100 ids;
// callers with more must chunk.
func (c *FileDetailsClient) GetFileDetails(ctx context.Context, ids []uint64) (map[uint64]FileDetails, error) {
	if len(ids) == 0 {
		return map[uint64]FileDetails{}, nil
	}
	if len(ids) > 100 {
		return nil, fmt.Errorf("product: GetFileDetails: %d ids exceeds Steam's 100-per-call limit", len(ids))
	}

	form := url.Values{}
	form.Set("itemcount", strconv.Itoa(len(ids)))
	for i, id := range ids {
		form.Set(fmt.Sprintf("publishedfileids[%d]", i), strconv.FormatUint(id, 10))
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, steamWebAPIURL, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, fmt.Errorf("product: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	client := c.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("product: GetPublishedFileDetails: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("product: GetPublishedFileDetails: HTTP %d", resp.StatusCode)
	}

	var parsed struct {
		Response struct {
			Result               int `json:"result"`
			ResultCount          int `json:"resultcount"`
			PublishedFileDetails []struct {
				PublishedFileID string      `json:"publishedfileid"`
				Result          int         `json:"result"`
				Title           string      `json:"title"`
				FileSize        json.Number `json:"file_size"` // the API sends a string for some items and a number for others
				TimeUpdated     int64       `json:"time_updated"`
			} `json:"publishedfiledetails"`
		} `json:"response"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return nil, fmt.Errorf("product: decode response: %w", err)
	}

	out := make(map[uint64]FileDetails, len(parsed.Response.PublishedFileDetails))
	for _, d := range parsed.Response.PublishedFileDetails {
		id, err := strconv.ParseUint(d.PublishedFileID, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("product: response publishedfileid %q: %w", d.PublishedFileID, err)
		}
		size, _ := d.FileSize.Int64() // a missing or odd size is not an error: it only serves as a sanity check
		out[id] = FileDetails{
			PublishedFileID: id,
			Result:          d.Result,
			Title:           d.Title,
			FileSize:        size,
			TimeUpdated:     d.TimeUpdated,
		}
	}
	return out, nil
}
