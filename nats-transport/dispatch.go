package natstransport

import (
	"bytes"
	"crypto/tls"
	"io"
	"math"
	"net/http"
	"net/url"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/telemetrytv/trace"
	"github.com/vmihailenco/msgpack/v5"
)

var (
	transportNatsDebug       = trace.Bind("zephyr:transport:nats")
	transportNatsDispatchDebug = trace.Bind("zephyr:transport:nats:dispatch")
)

const DispatchTimeout = 30 * time.Second
const DispatchBodyChunkSize = 1024 * 16

type TLS struct {
	Version            uint16 `msgpack:"version"`
	HandshakeComplete  bool   `msgpack:"handshakeComplete"`
	DidResume          bool   `msgpack:"didResume"`
	CipherSuite        uint16 `msgpack:"cipherSuite"`
	NegotiatedProtocol string `msgpack:"negotiatedProtocol"`
	ServerName         string `msgpack:"serverName"`
	// NOTE: Due to the complexity of the certificate structures, we are not
	//       including them in the JSON output for now.
	// PeerCertificates            []JSONCertificate   `msgpack:"peerCertificates"`
	// VerifiedChains              [][]JSONCertificate `msgpack:"verifiedChains"`
	SignedCertificateTimestamps [][]byte `msgpack:"signedCertificateTimestamps"`
	OCSPResponse                []byte   `msgpack:"ocspResponse"`
	TLSUnique                   []byte   `msgpack:"tlsUnique"`
}

type Request struct {
	Method           string              `msgpack:"method"`
	URL              string              `msgpack:"url"`
	Proto            string              `msgpack:"proto"`
	ProtoMajor       int                 `msgpack:"protoMajor"`
	ProtoMinor       int                 `msgpack:"protoMinor"`
	Header           map[string][]string `msgpack:"header"`
	ContentLength    int64               `msgpack:"contentLength"`
	TransferEncoding []string            `msgpack:"transferEncoding"`
	Host             string              `msgpack:"host"`
	Trailers         map[string][]string `msgpack:"trailers"`
	RemoteAddr       string              `msgpack:"remoteAddr"`
	RequestURI       string              `msgpack:"requestURI"`
	TLS              *TLS                `msgpack:"tls"`

	ResponseSubject     string `msgpack:"responseSubject"`
	ResponseBodySubject string `msgpack:"responseBodySubject"`
}

type RequestAck struct {
	RequestBodySubject string `msgpack:"requestBodySubject"`
}

type ResponseError struct {
	Message string `msgpack:"message"`
	Stack   string `msgpack:"stack"`
}

type Response struct {
	StatusCode int                 `msgpack:"statusCode"`
	Header     map[string][]string `msgpack:"header"`
	Error      string              `msgpack:"error"`
}

type BodyChunk struct {
	Index int    `msgpack:"index"`
	Data  []byte `msgpack:"data"`
	Error string `msgpack:"error"`
	IsEOF bool   `msgpack:"end"`
}

func (c *NatsTransport) Dispatch(serviceName string, res http.ResponseWriter, req *http.Request) error {
	transportNatsDispatchDebug.Tracef("Dispatching request to service %s: %s %s", serviceName, req.Method, req.URL.Path)
	
	requestSubject := namespace("service", serviceName)
	responseSubject := nats.NewInbox()
	responseBodySubject := nats.NewInbox()
	
	transportNatsDispatchDebug.Tracef("Using subjects - request: %s, response: %s, responseBody: %s", 
		requestSubject, responseSubject, responseBodySubject)

	request := &Request{
		Method:           req.Method,
		URL:              req.URL.String(),
		Proto:            req.Proto,
		ProtoMajor:       req.ProtoMajor,
		ProtoMinor:       req.ProtoMinor,
		Header:           req.Header,
		ContentLength:    req.ContentLength,
		TransferEncoding: req.TransferEncoding,
		Host:             req.Host,
		Trailers:         req.Trailer,
		RemoteAddr:       req.RemoteAddr,
		RequestURI:       req.RequestURI,

		ResponseSubject:     responseSubject,
		ResponseBodySubject: responseBodySubject,
	}

	if req.TLS != nil {
		transportNatsDebug.Trace("Request uses TLS, copying TLS state")
		request.TLS = &TLS{
			Version:                     req.TLS.Version,
			HandshakeComplete:           req.TLS.HandshakeComplete,
			DidResume:                   req.TLS.DidResume,
			CipherSuite:                 req.TLS.CipherSuite,
			NegotiatedProtocol:          req.TLS.NegotiatedProtocol,
			ServerName:                  req.TLS.ServerName,
			SignedCertificateTimestamps: req.TLS.SignedCertificateTimestamps,
			OCSPResponse:                req.TLS.OCSPResponse,
			TLSUnique:                   req.TLS.TLSUnique,
		}
	}

	transportNatsDispatchDebug.Trace("Marshaling request")
	requestBytes, err := msgpack.Marshal(request)
	if err != nil {
		transportNatsDispatchDebug.Tracef("Failed to marshal request: %v", err)
		return err
	}

	transportNatsDispatchDebug.Tracef("Setting up response subscription to %s", responseSubject)
	responseSub, err := c.NatsConnection.SubscribeSync(responseSubject)
	if err != nil {
		transportNatsDispatchDebug.Tracef("Failed to subscribe to response subject %s: %v", responseSubject, err)
		return err
	}
	defer func() {
		transportNatsDispatchDebug.Trace("Unsubscribing from response subject")
		if err := responseSub.Unsubscribe(); err != nil {
			transportNatsDispatchDebug.Tracef("Failed to unsubscribe from response subject: %v", err)
		}
	}()

	transportNatsDispatchDebug.Tracef("Setting up response body subscription to %s", responseBodySubject)
	responseBodySub, err := c.NatsConnection.SubscribeSync(responseBodySubject)
	if err != nil {
		transportNatsDispatchDebug.Tracef("Failed to subscribe to response body subject %s: %v", responseBodySubject, err)
		return err
	}
	defer func() {
		transportNatsDispatchDebug.Trace("Unsubscribing from response body subject")
		if err := responseBodySub.Unsubscribe(); err != nil {
			transportNatsDispatchDebug.Tracef("Failed to unsubscribe from response body subject: %v", err)
		}
	}()

	transportNatsDispatchDebug.Tracef("Sending request to %s", requestSubject)
	requestAckMsg, err := c.NatsConnection.Request(requestSubject, requestBytes, DispatchTimeout)
	if err != nil {
		transportNatsDispatchDebug.Tracef("Request to %s failed: %v", requestSubject, err)
		return err
	}
	
	transportNatsDispatchDebug.Trace("Received acknowledgment from service")
	requestAck := &RequestAck{}
	if err := msgpack.Unmarshal(requestAckMsg.Data, requestAck); err != nil {
		transportNatsDispatchDebug.Tracef("Failed to unmarshal request acknowledgment: %v", err)
		return err
	}

	requestBodySubject := requestAck.RequestBodySubject
	transportNatsDispatchDebug.Tracef("Using request body subject: %s", requestBodySubject)

	// Cover the case of a nil body reader
	reqBody := req.Body
	if reqBody == nil {
		transportNatsDispatchDebug.Trace("Request has nil body, using EOF reader")
		reqBody = &eofReader{}
	}

	transportNatsDispatchDebug.Trace("Streaming request body")
	i := 0
	for {
		requestBodyBytes := make([]byte, DispatchBodyChunkSize)
		lenRead, err := reqBody.Read(requestBodyBytes)
		isEOF := err == io.EOF
		if !isEOF && err != nil {
			transportNatsDispatchDebug.Tracef("Error reading request body: %v", err)
			return err
		}

		if lenRead != 0 {
			transportNatsDispatchDebug.Tracef("Sending request body chunk %d, size: %d bytes", i, lenRead)
			bodyChunk := &BodyChunk{
				Index: i,
				Data:  requestBodyBytes[:lenRead],
			}
			i += 1

			bodyChunkBytes, err := msgpack.Marshal(bodyChunk)
			if err != nil {
				transportNatsDispatchDebug.Tracef("Failed to marshal request body chunk: %v", err)
				return err
			}

			if err := c.NatsConnection.Publish(requestBodySubject, bodyChunkBytes); err != nil {
				transportNatsDispatchDebug.Tracef("Failed to publish request body chunk: %v", err)
				return err
			}
		}

		if isEOF {
			transportNatsDispatchDebug.Tracef("Sending EOF request body chunk %d", i)
			bodyChunk := &BodyChunk{
				Index: i,
				IsEOF: true,
			}

			bodyChunkBytes, err := msgpack.Marshal(bodyChunk)
			if err != nil {
				transportNatsDispatchDebug.Tracef("Failed to marshal EOF request body chunk: %v", err)
				return err
			}

			if err := c.NatsConnection.Publish(requestBodySubject, bodyChunkBytes); err != nil {
				transportNatsDispatchDebug.Tracef("Failed to publish EOF request body chunk: %v", err)
				return err
			}

			break
		}
	}
	transportNatsDispatchDebug.Trace("Finished streaming request body")

	transportNatsDispatchDebug.Trace("Waiting for response headers")
	responseMsg, err := responseSub.NextMsg(DispatchTimeout)
	if err != nil {
		transportNatsDispatchDebug.Tracef("Error waiting for response headers: %v", err)
		return err
	}
	
	transportNatsDispatchDebug.Trace("Unmarshaling response headers")
	response := &Response{}
	if err := msgpack.Unmarshal(responseMsg.Data, response); err != nil {
		transportNatsDispatchDebug.Tracef("Failed to unmarshal response: %v", err)
		return err
	}

	transportNatsDispatchDebug.Tracef("Setting response headers and status code: %d", response.StatusCode)
	for key, values := range response.Header {
		for _, value := range values {
			res.Header().Add(key, value)
		}
	}
	res.WriteHeader(response.StatusCode)

	transportNatsDispatchDebug.Trace("Reading response body chunks")
	for i := 0; true; i += 1 {
		transportNatsDispatchDebug.Tracef("Waiting for response body chunk %d", i)
		bodyChunkMsg, err := responseBodySub.NextMsg(DispatchTimeout)
		if err != nil {
			transportNatsDispatchDebug.Tracef("Error receiving response body chunk: %v", err)
			return err
		}

		bodyChunk := &BodyChunk{}
		if err := msgpack.Unmarshal(bodyChunkMsg.Data, bodyChunk); err != nil {
			transportNatsDispatchDebug.Tracef("Failed to unmarshal response body chunk: %v", err)
			return err
		}

		if _, err := res.Write(bodyChunk.Data); err != nil {
			transportNatsDispatchDebug.Tracef("Failed to write response body chunk: %v", err)
			return err
		}

		if bodyChunk.IsEOF {
			transportNatsDispatchDebug.Trace("Received final body chunk (EOF)")
			break
		}
	}

	transportNatsDispatchDebug.Trace("Dispatch completed successfully")
	return nil
}

func (c *NatsTransport) BindDispatch(serviceName string, handler func(res http.ResponseWriter, req *http.Request)) error {
	dispatchSubject := namespace("service", serviceName)
	sub, err := c.NatsConnection.QueueSubscribe(dispatchSubject, dispatchSubject, func(msg *nats.Msg) {
		if err := c.handleDispatch(msg, handler); err != nil {
			panic(err)
		}
	})
	if err != nil {
		return err
	}

	unbinders, ok := c.unbindDispatch[serviceName]
	if !ok {
		unbinders = []func() error{}
	}
	unbinders = append(unbinders, func() error {
		return sub.Unsubscribe()
	})
	c.unbindDispatch[serviceName] = unbinders

	return nil
}

type requestReader struct {
	natsSubscription *nats.Subscription
	hasEnded         bool
	buffer           bytes.Buffer
}

func (r *requestReader) Read(p []byte) (int, error) {
	if !r.hasEnded {
		for r.buffer.Len() < len(p) {
			bodyChunkMsg, err := r.natsSubscription.NextMsg(DispatchTimeout)
			if err != nil {
				return 0, err
			}
			bodyChunk := &BodyChunk{}
			if err := msgpack.Unmarshal(bodyChunkMsg.Data, bodyChunk); err != nil {
				return 0, err
			}

			if bodyChunk.IsEOF {
				r.hasEnded = true
				if err := r.natsSubscription.Unsubscribe(); err != nil {
					return 0, err
				}
				break
			}

			if _, err := r.buffer.Write(bodyChunk.Data); err != nil {
				return 0, err
			}
		}
	}

	return r.buffer.Read(p)
}

func (r *requestReader) Close() error {
	// TODO: currently is a noop, in future we may want to unsubscribe and
	// inform the other side that we are closing the connection
	return nil
}

type responseWriter struct {
	responseSubject     string
	responseBodySubject string
	natsConnection      *nats.Conn
	header              http.Header
	statusCode          int
	hasSentHeaders      bool
	writeIndex          int
	buffer              bytes.Buffer
	err                 error
}

func (r *responseWriter) Header() http.Header {
	return r.header
}

func (r *responseWriter) WriteHeader(statusCode int) {
	if r.statusCode != 0 {
		panic("cannot write headers twice")
	}
	r.statusCode = statusCode
}

func (r *responseWriter) Write(p []byte) (int, error) {
	if err := r.ensureHeadersSent(); err != nil {
		return 0, err
	}
	n, err := r.buffer.Write(p)
	if err != nil {
		return 0, err
	}
	chunkCount := int(math.Floor(float64(r.buffer.Len()) / DispatchBodyChunkSize))
	for i := 0; i < chunkCount; i += 1 {
		if err := r.writeChunk(); err != nil {
			return 0, err
		}
	}
	return n, nil
}

func (r *responseWriter) WriteError(err error) {
	r.err = err
}

func (r *responseWriter) End() error {
	if err := r.ensureHeadersSent(); err != nil {
		return err
	}
	if r.buffer.Len() > 0 {
		if err := r.writeChunk(); err != nil {
			return err
		}
	}

	bodyChunk := &BodyChunk{
		Index: r.writeIndex,
		IsEOF: true,
	}
	if r.err != nil {
		bodyChunk.Error = r.err.Error()
	}
	bodyChunkBytes, err := msgpack.Marshal(bodyChunk)
	if err != nil {
		return err
	}

	return r.natsConnection.Publish(r.responseBodySubject, bodyChunkBytes)
}

func (r *responseWriter) ensureHeadersSent() error {
	if r.hasSentHeaders {
		return nil
	}
	r.hasSentHeaders = true

	headers := map[string][]string{}
	for key, values := range r.header {
		headers[key] = values
	}
	response := &Response{
		StatusCode: r.statusCode,
		Header:     headers,
	}
	responseBytes, err := msgpack.Marshal(response)
	if err != nil {
		return err
	}

	return r.natsConnection.Publish(r.responseSubject, responseBytes)
}

func (r *responseWriter) writeChunk() error {
	bodyChunkBytes, err := msgpack.Marshal(&BodyChunk{
		Index: r.writeIndex,
		Data:  r.buffer.Next(DispatchBodyChunkSize),
	})
	if err != nil {
		return err
	}
	if err := r.natsConnection.Publish(r.responseBodySubject, bodyChunkBytes); err != nil {
		return err
	}
	r.writeIndex += 1
	return nil
}

func (c *NatsTransport) handleDispatch(msg *nats.Msg, handler func(res http.ResponseWriter, req *http.Request)) error {
	transportNatsDispatchDebug.Trace("Handling dispatched request")
	responseBodySubject := nats.NewInbox()

	request := &Request{}
	if err := msgpack.Unmarshal(msg.Data, request); err != nil {
		transportNatsDispatchDebug.Tracef("Failed to unmarshal request: %v", err)
		return err
	}

	transportNatsDispatchDebug.Tracef("Received request: %s %s", request.Method, request.URL)
	
	reqUrl, err := url.Parse(request.URL)
	if err != nil {
		transportNatsDispatchDebug.Tracef("Failed to parse URL: %v", err)
		return err
	}

	reqBodySubscription, err := c.NatsConnection.SubscribeSync(responseBodySubject)
	if err != nil {
		transportNatsDispatchDebug.Tracef("Failed to subscribe to request body subject: %v", err)
		return err
	}
	defer func() {
		transportNatsDispatchDebug.Trace("Unsubscribing from request body subject")
		if err := reqBodySubscription.Unsubscribe(); err != nil {
			transportNatsDispatchDebug.Tracef("Failed to unsubscribe from request body subject: %v", err)
		}
	}()

	reqReader := &requestReader{
		natsSubscription: reqBodySubscription,
		buffer:           bytes.Buffer{},
	}

	res := &responseWriter{
		responseSubject:     request.ResponseSubject,
		responseBodySubject: request.ResponseBodySubject,
		natsConnection:      c.NatsConnection,
		header:              map[string][]string{},
		buffer:              bytes.Buffer{},
	}
	
	transportNatsDispatchDebug.Trace("Set up response writer and request reader")

	req := &http.Request{
		Method:           request.Method,
		URL:              reqUrl,
		Proto:            request.Proto,
		ProtoMajor:       request.ProtoMajor,
		ProtoMinor:       request.ProtoMinor,
		Header:           request.Header,
		ContentLength:    request.ContentLength,
		TransferEncoding: request.TransferEncoding,
		Host:             request.Host,
		Trailer:          request.Trailers,
		RemoteAddr:       request.RemoteAddr,
		RequestURI:       request.RequestURI,
		Body:             reqReader,
	}

	if request.TLS != nil {
		req.TLS = &tls.ConnectionState{
			Version:                     request.TLS.Version,
			HandshakeComplete:           request.TLS.HandshakeComplete,
			DidResume:                   request.TLS.DidResume,
			CipherSuite:                 request.TLS.CipherSuite,
			NegotiatedProtocol:          request.TLS.NegotiatedProtocol,
			ServerName:                  request.TLS.ServerName,
			SignedCertificateTimestamps: request.TLS.SignedCertificateTimestamps,
			OCSPResponse:                request.TLS.OCSPResponse,
			TLSUnique:                   request.TLS.TLSUnique,
		}
	}

	requestAck := &RequestAck{
		RequestBodySubject: responseBodySubject,
	}
	requestAckBytes, err := msgpack.Marshal(requestAck)
	if err != nil {
		return err
	}
	if err := c.NatsConnection.Publish(msg.Reply, requestAckBytes); err != nil {
		return err
	}

	transportNatsDispatchDebug.Trace("Processing request with handler")
	func() {
		defer func() {
			if err := recover(); err != nil {
				transportNatsDispatchDebug.Tracef("Recovered from panic in handler: %v", err)
				res.WriteError(err.(error))
			}
		}()

		handler(res, req)
	}()
	
	transportNatsDispatchDebug.Trace("Handler completed, sending response")
	if err := res.End(); err != nil {
		transportNatsDispatchDebug.Tracef("Failed to end response: %v", err)
		return err
	}

	transportNatsDispatchDebug.Trace("Request handling completed successfully")
	return nil
}

func (c *NatsTransport) UnbindDispatch(serviceName string) error {
	unbinders, ok := c.unbindDispatch[serviceName]
	if ok {
		for _, unbind := range unbinders {
			if err := unbind(); err != nil {
				return err
			}
		}
		delete(c.unbindDispatch, serviceName)
	}
	return nil
}

type eofReader struct{}

var _ io.ReadCloser = &eofReader{}

func (r *eofReader) Read(p []byte) (int, error) {
	return 0, io.EOF
}

func (r *eofReader) Close() error {
	return nil
}
