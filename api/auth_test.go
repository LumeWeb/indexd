package api

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"go.sia.tech/core/types"
	"go.sia.tech/jape"
	"lukechampine.com/frand"
)

type mockStore struct{ tokens map[types.PublicKey]struct{} }

func (s *mockStore) HasAccount(_ context.Context, ak types.PublicKey) (bool, error) {
	_, found := s.tokens[ak]
	return found, nil
}

func TestAuth(t *testing.T) {
	const hostname = "indexer.sia.tech"
	sk := types.GeneratePrivateKey()
	s := &mockStore{tokens: map[types.PublicKey]struct{}{sk.PublicKey(): {}}}

	server := httptest.NewServer(wrapSignedAPI(hostname, s, map[string]authedHandler{
		"GET /foo": func(jc jape.Context, pk types.PublicKey) {
			if pk != sk.PublicKey() {
				httpWriteError(jc.ResponseWriter, "account key mismatch", http.StatusInternalServerError)
				return
			}
			jc.ResponseWriter.WriteHeader(http.StatusOK)
		},
	}))
	defer server.Close()

	doRequest := func(authFn func(req *http.Request)) (int, string) {
		t.Helper()
		req, err := http.NewRequest("GET", server.URL+"/foo", http.NoBody)
		if err != nil {
			t.Fatal(err)
		}

		authFn(req)
		resp, err := server.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()

		var bytes []byte
		if resp.StatusCode != http.StatusOK {
			var err error
			bytes, err = io.ReadAll(resp.Body)
			if err != nil {
				t.Fatal(err)
			}
		}
		return resp.StatusCode, string(bytes)
	}

	// assert unauthorized if no auth was set
	status, _ := doRequest(func(req *http.Request) {})
	if status != http.StatusUnauthorized {
		t.Fatal("unexpected", status)
	}

	// assert unauthorized if 'SiaIdx-ValidUntil' is invalid
	status, errorMsg := doRequest(func(req *http.Request) {
		val := url.Values{}
		val.Set(queryParamValidUntil, "notatimestamp")
		val.Set(queryParamCredential, "1")
		val.Set(queryParamSignature, "1")
		req.URL.RawQuery = val.Encode()
	})
	if status != http.StatusUnauthorized {
		t.Fatal("unexpected", status)
	} else if !strings.Contains(errorMsg, "must be a unix timestamp") {
		t.Fatal("unexpected", errorMsg)
	}

	// assert unauthorized if 'SiaIdx-Credential' is invalid
	cred := sk.PublicKey().String()
	validUntil := time.Now().Add(time.Hour)
	validUntilTs := fmt.Sprint(validUntil.Unix())
	status, errorMsg = doRequest(func(req *http.Request) {
		val := url.Values{}
		val.Set(queryParamValidUntil, validUntilTs)
		val.Set(queryParamCredential, cred+"a")
		val.Set(queryParamSignature, "1")
		req.URL.RawQuery = val.Encode()
	})
	if status != http.StatusUnauthorized {
		t.Fatal("unexpected", status)
	} else if !strings.Contains(errorMsg, "must be a valid public key") {
		t.Fatal("unexpected", errorMsg)
	}

	// assert unauthorized if 'SiaIdx-Signature' is invalid
	var sig types.Signature
	frand.Read(sig[:])
	status, errorMsg = doRequest(func(req *http.Request) {
		val := url.Values{}
		val.Set(queryParamValidUntil, validUntilTs)
		val.Set(queryParamCredential, cred)
		val.Set(queryParamSignature, sig.String()+"a")
		req.URL.RawQuery = val.Encode()
	})
	if status != http.StatusUnauthorized {
		t.Fatal("unexpected", status)
	} else if !strings.Contains(errorMsg, "must be a 64-byte hex string") {
		t.Fatal("unexpected", errorMsg)
	}

	// assert authorized if everything is valid
	sig = sk.SignHash(requestHash(hostname, validUntil))
	status, errorMsg = doRequest(func(req *http.Request) {
		val := url.Values{}
		val.Set(queryParamValidUntil, validUntilTs)
		val.Set(queryParamCredential, cred)
		val.Set(queryParamSignature, sig.String())
		req.URL.RawQuery = val.Encode()
	})
	if status != http.StatusOK {
		t.Fatal("unexpected", status, errorMsg)
	}

	// assert unauthorized if the timestamp is in the past
	status, errorMsg = doRequest(func(req *http.Request) {
		val := url.Values{}
		val.Set(queryParamValidUntil, fmt.Sprint(time.Now().Unix()-1)) // still invalid
		val.Set(queryParamCredential, cred)
		val.Set(queryParamSignature, sig.String())
		req.URL.RawQuery = val.Encode()
	})
	if status != http.StatusUnauthorized {
		t.Fatal("unexpected", status)
	} else if !strings.Contains(errorMsg, ErrUnauthorized.Error()) {
		t.Fatal("unexpected", errorMsg)
	}

	// assert unauthorized if the account is unknown
	s.tokens = map[types.PublicKey]struct{}{}
	status, errorMsg = doRequest(func(req *http.Request) {
		val := url.Values{}
		val.Set(queryParamValidUntil, validUntilTs)
		val.Set(queryParamCredential, cred)
		val.Set(queryParamSignature, sig.String())
		req.URL.RawQuery = val.Encode()
	})
	if status != http.StatusUnauthorized {
		t.Fatal("unexpected", status)
	} else if !strings.Contains(errorMsg, ErrUnauthorized.Error()) {
		t.Fatal("unexpected", errorMsg)
	}
	s.tokens[sk.PublicKey()] = struct{}{}
}
