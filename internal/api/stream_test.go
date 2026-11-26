package api

import (
	"bufio"
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestSSETicksHaveIncreasingIDs(t *testing.T) {
	s := newTestServer(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s.Start(ctx)

	ts := httptest.NewServer(s)
	defer ts.Close()

	reqCtx, reqCancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer reqCancel()
	req, _ := http.NewRequestWithContext(reqCtx, http.MethodGet, ts.URL+"/api/stream", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/event-stream") {
		t.Fatalf("content-type = %q", ct)
	}

	scanner := bufio.NewScanner(resp.Body)
	var ids []int64
	var lastID int64
	for len(ids) < 2 && scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "id: ") {
			continue
		}
		id, err := strconv.ParseInt(strings.TrimPrefix(line, "id: "), 10, 64)
		if err != nil {
			t.Fatalf("bad id line %q: %v", line, err)
		}
		if id <= lastID {
			t.Fatalf("ID did not increase: %d after %d", id, lastID)
		}
		lastID = id
		ids = append(ids, id)
	}
	if len(ids) != 2 {
		t.Fatalf("got %d events, want 2", len(ids))
	}
}

func TestSSEReplaysAfterLastEventID(t *testing.T) {
	s := newTestServer(t)
	for i := 0; i < 3; i++ {
		s.hub.Publish("unit", "run-1", "", map[string]int{"i": i})
	}

	ts := httptest.NewServer(s)
	defer ts.Close()

	reqCtx, reqCancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer reqCancel()
	req, _ := http.NewRequestWithContext(reqCtx, http.MethodGet, ts.URL+"/api/stream", nil)
	req.Header.Set("Last-Event-ID", "1")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	scanner := bufio.NewScanner(resp.Body)
	var ids []int64
	for len(ids) < 2 && scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "id: ") {
			id, _ := strconv.ParseInt(strings.TrimPrefix(line, "id: "), 10, 64)
			ids = append(ids, id)
		}
	}
	if len(ids) != 2 || ids[0] != 2 || ids[1] != 3 {
		t.Fatalf("replay ids = %v, want [2 3]", ids)
	}
}
