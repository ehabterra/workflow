package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func newTestServer(t *testing.T) (*httptest.Server, *App) {
	t.Helper()
	app, _ := newTestApp(t)
	srv, err := NewServer(app)
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)
	return ts, app
}

// postForm posts without an html Accept header, i.e. as a webhook/API
// caller: we get status codes and plain text, not redirects.
func postForm(t *testing.T, url string, form url.Values) (int, string) {
	t.Helper()
	resp, err := http.PostForm(url, form)
	if err != nil {
		t.Fatalf("POST %s: %v", url, err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(body)
}

func submitViaHTTP(t *testing.T, ts *httptest.Server, submitter, amount string) string {
	t.Helper()
	// Don't follow the redirect: the Location header carries the new ID.
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	resp, err := client.PostForm(ts.URL+"/expenses", url.Values{
		"submitter":   {submitter},
		"description": {"via http"},
		"amount":      {amount},
	})
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("submit: status %d: %s", resp.StatusCode, body)
	}
	loc := resp.Header.Get("Location")
	return strings.TrimPrefix(loc, "/expenses/")
}

// TestFullLifecycle drives the real HTTP surface end to end: submit →
// parallel approvals → auto-finalize → payment enqueue → batch run → paid,
// with the dashboard, detail page, and metrics reflecting each step.
func TestFullLifecycle(t *testing.T) {
	ts, _ := newTestServer(t)
	id := submitViaHTTP(t, ts, "alice", "150.50")

	code, body := postForm(t, ts.URL+"/expenses/"+id+"/approve", url.Values{"branch": {"legal"}})
	if code != http.StatusOK || !strings.Contains(body, "legal_approve") {
		t.Fatalf("legal approve: %d %q", code, body)
	}
	code, body = postForm(t, ts.URL+"/expenses/"+id+"/approve", url.Values{"branch": {"finance"}})
	if code != http.StatusOK || !strings.Contains(body, "finalize") {
		t.Fatalf("finance approve must finalize: %d %q", code, body)
	}

	code, body = postForm(t, ts.URL+"/batch/run", nil)
	if code != http.StatusOK || !strings.Contains(body, "paid 1 expense(s), 0 held") {
		t.Fatalf("batch run: %d %q", code, body)
	}

	resp, err := http.Get(ts.URL + "/expenses/" + id)
	if err != nil {
		t.Fatal(err)
	}
	page, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(page), "paid") || !strings.Contains(string(page), "mark_paid") {
		t.Fatalf("detail page must show paid state and audit trail, got:\n%s", page)
	}

	resp, err = http.Get(ts.URL + "/metrics")
	if err != nil {
		t.Fatal(err)
	}
	metrics, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	for _, want := range []string{`transition="submit"`, `transition="finalize"`, `transition="pay"`} {
		if !strings.Contains(string(metrics), want) {
			t.Fatalf("metrics missing %s:\n%s", want, metrics)
		}
	}
}

// TestWebhookRedelivery is the M3.4 acceptance scenario: a redelivered
// approval is a 200 no-op, not an error and not a double fire.
func TestWebhookRedelivery(t *testing.T) {
	ts, _ := newTestServer(t)
	id := submitViaHTTP(t, ts, "bob", "80")

	code, _ := postForm(t, ts.URL+"/expenses/"+id+"/approve", url.Values{"branch": {"legal"}})
	if code != http.StatusOK {
		t.Fatalf("first delivery: %d", code)
	}
	code, body := postForm(t, ts.URL+"/expenses/"+id+"/approve", url.Values{"branch": {"legal"}})
	if code != http.StatusOK || !strings.Contains(body, "already processed") {
		t.Fatalf("redelivery must be a 200 no-op, got %d %q", code, body)
	}
}

// TestRejectIsTerminal: after any rejection the host treats the expense as
// closed — later decisions on either branch get 409.
func TestRejectIsTerminal(t *testing.T) {
	ts, _ := newTestServer(t)
	id := submitViaHTTP(t, ts, "carol", "60")

	code, body := postForm(t, ts.URL+"/expenses/"+id+"/reject", url.Values{"branch": {"legal"}})
	if code != http.StatusOK || !strings.Contains(body, "legal_reject") {
		t.Fatalf("reject: %d %q", code, body)
	}
	code, body = postForm(t, ts.URL+"/expenses/"+id+"/approve", url.Values{"branch": {"finance"}})
	if code != http.StatusConflict {
		t.Fatalf("approve after reject must 409, got %d %q", code, body)
	}
	code, _ = postForm(t, ts.URL+"/expenses/"+id+"/reject", url.Values{"branch": {"finance"}})
	if code != http.StatusConflict {
		t.Fatalf("second reject must 409, got %d", code)
	}
}

// TestPaymentGuardHoldsLargeAmounts: the CPN guard (token.amount <= 5000)
// keeps big expenses in payable for manual review.
func TestPaymentGuardHoldsLargeAmounts(t *testing.T) {
	ts, _ := newTestServer(t)
	id := submitViaHTTP(t, ts, "dave", "9000")

	for _, branch := range []string{"legal", "finance"} {
		if code, body := postForm(t, ts.URL+"/expenses/"+id+"/approve", url.Values{"branch": {branch}}); code != http.StatusOK {
			t.Fatalf("approve %s: %d %q", branch, code, body)
		}
	}
	code, body := postForm(t, ts.URL+"/batch/run", nil)
	if code != http.StatusOK || !strings.Contains(body, "paid 0 expense(s), 1 held") {
		t.Fatalf("guard must hold the 9000 expense: %d %q", code, body)
	}
}

func TestUnknownExpenseIs404(t *testing.T) {
	ts, _ := newTestServer(t)
	code, _ := postForm(t, ts.URL+"/expenses/exp-nope/approve", url.Values{"branch": {"legal"}})
	if code != http.StatusNotFound {
		t.Fatalf("want 404, got %d", code)
	}
	resp, err := http.Get(ts.URL + "/expenses/exp-nope")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("detail of unknown expense: want 404, got %d", resp.StatusCode)
	}
}
