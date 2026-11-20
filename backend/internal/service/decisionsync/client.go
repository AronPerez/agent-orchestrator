package decisionsync

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// Request timeouts (plan decision 13).
const (
	connectTimeout = 10 * time.Second
	requestTimeout = 30 * time.Second
	// waitSlack is added to a waiting exchange's own wait.
	waitSlack = 15 * time.Second
	// maxResponseBytes bounds a peer's response body.
	maxResponseBytes = 8 << 20
	// expectedHostHeader pins a request to the peer's identity, as remote CLI
	// targets do; the peer refuses it if the address now belongs to another host.
	expectedHostHeader = "X-AO-Expected-Host-ID"
)

// RemoteError is a failed request to a peer, classified for the peer's
// status row. Its message never carries a credential or the peer's text.
type RemoteError struct {
	Code    string // one of the Error* codes
	Status  int
	APICode string
}

func (e *RemoteError) Error() string {
	if e.Status == 0 {
		return "decision sync peer " + e.Code
	}
	if e.APICode != "" {
		return fmt.Sprintf("decision sync peer answered HTTP %d (%s)", e.Status, e.APICode)
	}
	return fmt.Sprintf("decision sync peer answered HTTP %d", e.Status)
}

// ErrorCode classifies any sync error into a peer error code.
func ErrorCode(err error) string {
	var remote *RemoteError
	switch {
	case err == nil:
		return ""
	case errors.As(err, &remote):
		return remote.Code
	case errors.Is(err, ErrPeerIdentityChanged):
		return ErrorIdentityChanged
	case errors.Is(err, context.DeadlineExceeded), errors.Is(err, context.Canceled):
		return ErrorUnreachable
	default:
		return ErrorServer
	}
}

// HTTPClient talks to peers' remote listeners.
type HTTPClient struct {
	http *http.Client
}

// NewHTTPClient returns a client over c, or over a default client with a
// 10-second connect timeout when c is nil. Requests set their own deadlines.
func NewHTTPClient(c *http.Client) *HTTPClient {
	if c == nil {
		c = &http.Client{Transport: &http.Transport{
			Proxy:                 http.ProxyFromEnvironment,
			DialContext:           (&net.Dialer{Timeout: connectTimeout, KeepAlive: 30 * time.Second}).DialContext,
			TLSHandshakeTimeout:   connectTimeout,
			MaxIdleConnsPerHost:   2,
			IdleConnTimeout:       90 * time.Second,
			ExpectContinueTimeout: time.Second,
		}}
	}
	copied := *c
	// Never follow a redirect: it could carry the bearer to another host.
	copied.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &HTTPClient{http: &copied}
}

// NormalizePeerURL returns a peer's base URL without a trailing slash. It
// refuses credentials, queries and fragments in the URL, so a token can never
// ride in one.
func NormalizePeerURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw != "" && !strings.Contains(raw, "://") {
		raw = "http://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return "", errors.New("the machine's address must be an http or https URL such as http://mac-mini.local:3011")
	}
	if u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return "", errors.New("the machine's address must not contain a user name, query or fragment")
	}
	return strings.TrimRight(u.Scheme+"://"+u.Host+u.Path, "/"), nil
}

// Identity probes a peer's unauthenticated identity route (ADR 0003). It
// sends no credential.
func (c *HTTPClient) Identity(ctx context.Context, baseURL string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/api/v1/identity", http.NoBody)
	if err != nil {
		return "", err
	}
	var identity struct {
		HostID string `json:"hostId"`
	}
	if err := c.do(req, &identity); err != nil {
		return "", err
	}
	if !strings.HasPrefix(identity.HostID, "h_") {
		return "", &RemoteError{Code: ErrorIncompatible, Status: http.StatusOK, APICode: "NO_HOST_ID"}
	}
	return identity.HostID, nil
}

// MintRequest is what this machine asks a peer to mint a sync token for.
type MintRequest struct {
	Name     string `json:"name"`
	Platform string `json:"platform"`
	ClientID string `json:"clientId"`
}

// MintResult is the peer's minted sync token.
type MintResult struct {
	HostID   string `json:"hostId"`
	DeviceID string `json:"deviceId"`
	Token    string `json:"token"`
	Scope    string `json:"scope"`
}

// MintToken asks the peer to mint a decision-sync token, presenting a full
// credential once. The credential is not kept.
func (c *HTTPClient) MintToken(ctx context.Context, baseURL, expectedHostID, credential string, in MintRequest) (MintResult, error) {
	var out MintResult
	if err := c.post(ctx, requestTimeout, baseURL+"/api/v1/remote-host/decision-sync-token", expectedHostID, credential, in, &out); err != nil {
		return MintResult{}, err
	}
	if out.HostID != expectedHostID || out.Token == "" || out.DeviceID == "" {
		return MintResult{}, &RemoteError{Code: ErrorIdentityChanged, Status: http.StatusOK, APICode: "UNEXPECTED_MINT"}
	}
	return out, nil
}

// RevokeOwnToken asks the peer to revoke the sync token presented. It probes
// the peer's identity first and sends nothing if the address now belongs to
// another machine.
func (c *HTTPClient) RevokeOwnToken(ctx context.Context, peer StoredPeer) error {
	hostID, err := c.Identity(ctx, peer.URL)
	if err != nil {
		return err
	}
	if hostID != peer.HostID {
		return fmt.Errorf("%w: expected %s, got %s", ErrPeerIdentityChanged, peer.HostID, hostID)
	}
	ctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete,
		peer.URL+"/api/v1/remote-host/decision-sync-token/"+url.PathEscape(peer.DeviceID), http.NoBody)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+peer.Token)
	req.Header.Set(expectedHostHeader, peer.HostID)
	return c.do(req, nil)
}

// Remote returns peer as the engine's Remote. Before its first request it
// probes the peer's identity and refuses to send the token to a machine with
// another host id.
func (c *HTTPClient) Remote(peer StoredPeer) Remote {
	return &httpRemote{client: c, peer: peer}
}

type httpRemote struct {
	client *HTTPClient
	peer   StoredPeer
	probed bool
}

func (r *httpRemote) probe(ctx context.Context) error {
	if r.probed {
		return nil
	}
	hostID, err := r.client.Identity(ctx, r.peer.URL)
	if err != nil {
		return err
	}
	if hostID != r.peer.HostID {
		return fmt.Errorf("%w: expected %s, got %s", ErrPeerIdentityChanged, r.peer.HostID, hostID)
	}
	r.probed = true
	return nil
}

// Exchange implements Remote.
func (r *httpRemote) Exchange(ctx context.Context, req ExchangeRequest) (ExchangeResponse, error) {
	if err := r.probe(ctx); err != nil {
		return ExchangeResponse{}, err
	}
	timeout := requestTimeout
	if req.WaitSeconds > 0 {
		timeout = time.Duration(min(req.WaitSeconds, MaxWaitSeconds))*time.Second + waitSlack
	}
	have := map[string]int64(req.Have)
	if have == nil {
		have = map[string]int64{}
	}
	var out WireExchangeResponse
	if err := r.client.post(ctx, timeout, r.peer.URL+"/api/v1/decision-sync/exchange", r.peer.HostID, r.peer.Token, WireExchangeRequest{
		Protocol: req.Protocol, HostID: req.HostID, MachineLabel: req.MachineLabel,
		Have: have, Heads: headsToWire(req.Heads), WaitSeconds: req.WaitSeconds,
	}, &out); err != nil {
		return ExchangeResponse{}, err
	}
	return ExchangeResponse{
		Protocol: out.Protocol, HostID: out.HostID, MachineLabel: out.MachineLabel,
		Entries: FromWireEntries(out.Entries), More: out.More, Have: out.Have,
		Heads: headsFromWire(out.Heads), ServerTime: out.ServerTime,
	}, nil
}

// OriginIDs implements Remote.
func (r *httpRemote) OriginIDs(ctx context.Context, req OriginIDsRequest) (OriginIDsResponse, error) {
	if err := r.probe(ctx); err != nil {
		return OriginIDsResponse{}, err
	}
	var out WireOriginIDsResponse
	if err := r.client.post(ctx, requestTimeout, r.peer.URL+"/api/v1/decision-sync/origin-ids", r.peer.HostID, r.peer.Token, WireOriginIDsRequest{
		Protocol: req.Protocol, HostID: req.HostID, Origin: req.Origin, From: req.From, Limit: req.Limit,
	}, &out); err != nil {
		return OriginIDsResponse{}, err
	}
	return OriginIDsResponse{HostID: out.HostID, IDs: out.IDs, More: out.More}, nil
}

// Push implements Remote.
func (r *httpRemote) Push(ctx context.Context, req PushRequest) (PushResponse, error) {
	if err := r.probe(ctx); err != nil {
		return PushResponse{}, err
	}
	var out WirePushResponse
	if err := r.client.post(ctx, requestTimeout, r.peer.URL+"/api/v1/decision-sync/entries", r.peer.HostID, r.peer.Token, WirePushRequest{
		Protocol: req.Protocol, HostID: req.HostID, Entries: ToWireEntries(req.Entries), Heads: headsToWire(req.Heads),
	}, &out); err != nil {
		return PushResponse{}, err
	}
	result := domain.DecisionApplyResult{Applied: out.Applied, Duplicates: out.Duplicates}
	for _, g := range out.Gaps {
		result.Gaps = append(result.Gaps, domain.DecisionSyncGap{Origin: g.Origin, Have: g.Have, Got: g.Got})
	}
	for _, c := range out.Conflicts {
		result.Conflicts = append(result.Conflicts, domain.DecisionSyncConflict{
			Origin: c.Origin, OriginSeq: c.OriginSeq, LocalEntryID: c.LocalEntryID,
			RemoteEntryID: c.RemoteEntryID, PeerHostID: c.PeerHostID, DetectedAt: c.DetectedAt,
		})
	}
	return PushResponse{Result: result, Have: out.Have, Heads: headsFromWire(out.Heads)}, nil
}

func (c *HTTPClient) post(ctx context.Context, timeout time.Duration, target, expectedHostID, bearer string, in, out any) error {
	var body bytes.Buffer
	enc := json.NewEncoder(&body)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(in); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target, &body)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+bearer)
	req.Header.Set(expectedHostHeader, expectedHostID)
	return c.do(req, out)
}

func (c *HTTPClient) do(req *http.Request, out any) error {
	resp, err := c.http.Do(req) // #nosec G704 -- the target is a machine the person linked explicitly.
	if err != nil {
		if ctxErr := req.Context().Err(); ctxErr != nil && !errors.Is(ctxErr, context.DeadlineExceeded) {
			return ctxErr
		}
		return &RemoteError{Code: ErrorUnreachable}
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return classifyResponse(resp)
	}
	if out == nil {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxResponseBytes))
		return nil
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxResponseBytes)).Decode(out); err != nil {
		return &RemoteError{Code: ErrorServer, Status: resp.StatusCode, APICode: "UNREADABLE_RESPONSE"}
	}
	return nil
}

func classifyResponse(resp *http.Response) error {
	var envelope struct {
		Code string `json:"code"`
	}
	_ = json.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(&envelope)
	e := &RemoteError{Status: resp.StatusCode, APICode: envelope.Code}
	switch {
	case resp.StatusCode == http.StatusUnauthorized && envelope.Code == "DEVICE_REVOKED":
		e.Code = ErrorRevoked
	case resp.StatusCode == http.StatusUnauthorized, resp.StatusCode == http.StatusForbidden:
		e.Code = ErrorUnauthorized
	case resp.StatusCode == http.StatusNotFound, resp.StatusCode == http.StatusMethodNotAllowed:
		e.Code = ErrorIncompatible
	case resp.StatusCode == http.StatusMisdirectedRequest, envelope.Code == "DECISION_SYNC_SAME_IDENTITY":
		e.Code = ErrorIdentityChanged
	case resp.StatusCode == http.StatusTooManyRequests, resp.StatusCode == http.StatusServiceUnavailable,
		resp.StatusCode == http.StatusBadGateway, resp.StatusCode == http.StatusGatewayTimeout:
		e.Code = ErrorUnreachable
	default:
		e.Code = ErrorServer
	}
	return e
}
