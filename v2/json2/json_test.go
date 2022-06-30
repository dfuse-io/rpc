// Copyright 2009 The Go Authors. All rights reserved.
// Copyright 2012 The Gorilla Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package json2

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/gorilla/rpc/v2"
	"github.com/stretchr/testify/require"
)

// ResponseRecorder is an implementation of http.ResponseWriter that
// records its mutations for later inspection in tests.
type ResponseRecorder struct {
	Code      int           // the HTTP response code from WriteHeader
	HeaderMap http.Header   // the HTTP response headers
	Body      *bytes.Buffer // if non-nil, the bytes.Buffer to append written data to
	Flushed   bool
}

// NewRecorder returns an initialized ResponseRecorder.
func NewRecorder() *ResponseRecorder {
	return &ResponseRecorder{
		HeaderMap: make(http.Header),
		Body:      new(bytes.Buffer),
	}
}

// DefaultRemoteAddr is the default remote address to return in RemoteAddr if
// an explicit DefaultRemoteAddr isn't set on ResponseRecorder.
const DefaultRemoteAddr = "1.2.3.4"

// Header returns the response headers.
func (rw *ResponseRecorder) Header() http.Header {
	return rw.HeaderMap
}

// Write always succeeds and writes to rw.Body, if not nil.
func (rw *ResponseRecorder) Write(buf []byte) (int, error) {
	if rw.Body != nil {
		rw.Body.Write(buf)
	}
	if rw.Code == 0 {
		rw.Code = http.StatusOK
	}
	return len(buf), nil
}

// WriteHeader sets rw.Code.
func (rw *ResponseRecorder) WriteHeader(code int) {
	rw.Code = code
}

// Flush sets rw.Flushed to true.
func (rw *ResponseRecorder) Flush() {
	rw.Flushed = true
}

// ----------------------------------------------------------------------------

var ErrResponseError = errors.New("response error")
var ErrMappedResponseError = errors.New("mapped response error")

type Service1Request struct {
	A int
	B int
}

type Service1NoParamsRequest struct {
	V  string `json:"jsonrpc"`
	M  string `json:"method"`
	ID uint64 `json:"id"`
}

type Service1ParamsArrayRequest struct {
	V string `json:"jsonrpc"`
	P []struct {
		T string
	} `json:"params"`
	M  string `json:"method"`
	ID uint64 `json:"id"`
}

type Service1Response struct {
	Result int
}

type Service1 struct {
}

const Service1DefaultResponse = 9999

func (t *Service1) Multiply(r *http.Request, req *Service1Request, res *Service1Response) error {
	if req.A == 0 && req.B == 0 {
		// Sentinel value for test with no params.
		res.Result = Service1DefaultResponse
	} else {
		res.Result = req.A * req.B
	}
	return nil
}

func (t *Service1) ResponseError(r *http.Request, req *Service1Request, res *Service1Response) error {
	return ErrResponseError
}

func (t *Service1) MappedResponseError(r *http.Request, req *Service1Request, res *Service1Response) error {
	return ErrMappedResponseError
}

func execute(t *testing.T, s *rpc.Server, method string, req interface{}) ([]*clientResponse, error) {
	if !s.HasMethod(method) {
		t.Fatal("Expected to be registered:", method)
	}

	buf, _ := EncodeClientRequest(method, req)
	body := bytes.NewBuffer(buf)
	r, _ := http.NewRequest("POST", "http://localhost:8080/", body)
	r.Header.Set("Content-Type", "application/json")

	w := NewRecorder()
	s.ServeHTTP(w, r)

	return DecodeClientResponse(t, w.Body)
}

// DecodeClientResponse decodes the response body of a client request into
// the interface reply.
func DecodeClientResponse(t *testing.T, r io.Reader) ([]*clientResponse, error) {
	t.Helper()
	data, err := io.ReadAll(r)
	if err != nil {
		return nil, fmt.Errorf("reading response body: %w", err)
	}
	raw := json.RawMessage(data)
	fmt.Println(string(raw))
	c := &clientResponse{}
	if !isBatch(raw) {
		err = json.Unmarshal(data, &c)
		if err != nil {
			return nil, fmt.Errorf("decoding none batch response body: %w", err)
		}

		return []*clientResponse{c}, nil
	}

	var cr []*clientResponse
	err = json.Unmarshal(data, &cr)
	if err != nil {
		return nil, fmt.Errorf("decoding batch response body: %w", err)
	}

	return cr, nil
}

func executeRaw(t *testing.T, s *rpc.Server, req interface{}) ([]*clientResponse, error) {
	j, _ := json.Marshal(req)
	r, _ := http.NewRequest("POST", "http://localhost:8080/", bytes.NewBuffer(j))
	r.Header.Set("Content-Type", "application/json")

	w := NewRecorder()
	s.ServeHTTP(w, r)

	return DecodeClientResponse(t, w.Body)
}

func executeInvalidJSON(t *testing.T, s *rpc.Server) ([]*clientResponse, error) {
	r, _ := http.NewRequest("POST", "http://localhost:8080/", strings.NewReader(`not even a json`))
	r.Header.Set("Content-Type", "application/json")

	w := NewRecorder()
	s.ServeHTTP(w, r)

	return DecodeClientResponse(t, w.Body)
}

func TestService(t *testing.T) {
	s := rpc.NewServer()
	s.RegisterCodec(NewCodec(), "application/json")
	s.RegisterService(new(Service1), "")

	var res Service1Response
	cr, err := execute(t, s, "Service1.Multiply", &Service1Request{4, 2})
	require.NoError(t, err)
	require.Nil(t, cr[0].Error)

	err = json.Unmarshal(*cr[0].Result, &res)
	require.NoError(t, err)
	require.Equal(t, 8, res.Result)

	cr, err = execute(t, s, "Service1.ResponseError", &Service1Request{4, 2})
	require.NoError(t, err)
	require.NotNil(t, cr[0].Error)

	cr, err = executeRaw(t, s, &Service1NoParamsRequest{"2.0", "Service1.Multiply", 1})
	require.NoError(t, err)
	// No parameters.
	res = Service1Response{}
	err = json.Unmarshal(*cr[0].Result, &res)
	require.NoError(t, err)

	if res.Result != Service1DefaultResponse {
		t.Errorf("Wrong response: got %v, want %v", res.Result, Service1DefaultResponse)
	}

	// Parameters as by-position.
	res = Service1Response{}
	req := Service1ParamsArrayRequest{
		V: "2.0",
		P: []struct {
			T string
		}{{
			T: "test",
		}},
		M:  "Service1.Multiply",
		ID: 1,
	}
	cr, err = executeRaw(t, s, &req)
	require.NoError(t, err)

	err = json.Unmarshal(*cr[0].Result, &res)
	require.NoError(t, err)
	require.Equal(t, Service1DefaultResponse, res.Result)

	res = Service1Response{}
	cr, err = executeInvalidJSON(t, s)
	require.NoError(t, err)
	jsonRpcErr := &Error{}
	err = json.Unmarshal(*cr[0].Error, &jsonRpcErr)
	require.NoError(t, err)
	require.Equal(t, E_PARSE, jsonRpcErr.Code)
}

func TestServiceBatch(t *testing.T) {
	s := rpc.NewServer()
	s.RegisterCodec(NewCodec(), "application/json")
	s.RegisterService(new(Service1), "")

	res := Service1Response{}
	req := []*Service1ParamsArrayRequest{
		{
			V: "2.0",
			P: []struct {
				T string
			}{{
				T: "test",
			}},
			M:  "Service1.Multiply",
			ID: 1,
		}, {
			V: "2.0",
			P: []struct {
				T string
			}{{
				T: "test",
			}},
			M:  "Service1.Multiply",
			ID: 2,
		},
	}

	res = Service1Response{}

	cr, err := executeRaw(t, s, &req)
	require.NoError(t, err)

	err = json.Unmarshal(*cr[0].Result, &res)
	require.NoError(t, err)
	require.Equal(t, Service1DefaultResponse, res.Result)

	err = json.Unmarshal(*cr[1].Result, &res)
	require.NoError(t, err)
	require.Equal(t, Service1DefaultResponse, res.Result)
}

func TestServiceWithErrorMapper(t *testing.T) {
	const mappedErrorCode = 100

	// errorMapper maps ErrMappedResponseError to an Error with mappedErrorCode Code, everything else is returned as-is
	errorMapper := func(ctx context.Context, err error) error {
		if err == ErrMappedResponseError {
			return &Error{
				Code:    mappedErrorCode,
				Message: err.Error(),
			}
		}
		// Map everything else to E_SERVER
		return &Error{
			Code:    E_SERVER,
			Message: err.Error(),
		}
	}

	s := rpc.NewServer()
	s.RegisterCodec(NewCustomCodec(WithErrorMapper(errorMapper)), "application/json")
	s.RegisterService(new(Service1), "")

	//var res Service1Response
	jsonRpcErr := &Error{}
	cr, err := execute(t, s, "Service1.MappedResponseError", &Service1Request{4, 2})
	require.NoError(t, err)
	err = json.Unmarshal(*cr[0].Error, &jsonRpcErr)
	require.NoError(t, err)

	require.Equal(t, ErrorCode(mappedErrorCode), jsonRpcErr.Code)
	require.Equal(t, ErrMappedResponseError.Error(), jsonRpcErr.Message)

	// Unmapped error behaves as usual
	cr, err = execute(t, s, "Service1.ResponseError", &Service1Request{4, 2})
	require.NoError(t, err)

	err = json.Unmarshal(*cr[0].Error, &jsonRpcErr)
	require.Equal(t, E_SERVER, jsonRpcErr.Code)
	require.Equal(t, ErrResponseError.Error(), jsonRpcErr.Message)

	// Malformed request without method: our framework tries to return an error: we shouldn't map that one
	malformedRequest := struct {
		V  string `json:"jsonrpc"`
		ID string `json:"id"`
	}{
		V:  "3.0",
		ID: "any",
	}
	cr, err = executeRaw(t, s, &malformedRequest)
	require.NoError(t, err)
}
