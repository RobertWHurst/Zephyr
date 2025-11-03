package zephyr_test

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/RobertWHurst/navaros"
	"github.com/RobertWHurst/zephyr"
	localtransport "github.com/RobertWHurst/zephyr/local-transport"
	"github.com/stretchr/testify/assert"
)

func TestService(t *testing.T) {
	t.Run("Will send request to service handler, and send response back", func(t *testing.T) {
		transport := localtransport.New()

		var recvRequest *http.Request
		handler := func(ctx *navaros.Context) {
			ctx.Headers.Set("Test-Header", "test-value")
			ctx.Status = 200
			ctx.Body = "test response"
			recvRequest = ctx.Request()
		}
		s := zephyr.NewService("testService", transport, handler)

		err := s.Start()
		assert.NoError(t, err)

		sentResponseWriter := httptest.NewRecorder()
		sentReader := bytes.NewBufferString("test body")
		sentRequest := httptest.NewRequest("POST", "http://server.url", sentReader)
		err = transport.Dispatch("testService", sentResponseWriter, sentRequest)
		assert.NoError(t, err)

		recvBody, err := io.ReadAll(recvRequest.Body)
		assert.NoError(t, err)
		assert.Equal(t, "test body", string(recvBody))
		assert.Equal(t, "POST", recvRequest.Method)
		assert.Equal(t, "http://server.url", recvRequest.URL.String())

		sentResponse := sentResponseWriter.Result()
		assert.Equal(t, 200, sentResponse.StatusCode)
		assert.Equal(t, "test-value", sentResponse.Header.Get("Test-Header"))
		sendResponseBody, err := io.ReadAll(sentResponse.Body)
		assert.NoError(t, err)
		assert.Equal(t, "test response", string(sendResponseBody))
	})
}

func TestService_Start(t *testing.T) {
	t.Run("Announces to gateway", func(t *testing.T) {
		transport := localtransport.New()
		s := zephyr.NewService("testService", transport, nil)

		var serviceDescriptor *zephyr.ServiceDescriptor
		err := transport.BindServiceAnnounce(func(d *zephyr.ServiceDescriptor) {
			serviceDescriptor = d
		})
		assert.NoError(t, err)

		err = s.Start()
		assert.NoError(t, err)
		assert.Equal(t, "testService", serviceDescriptor.Name)
	})

	t.Run("Listens for gateway announce, and re-anounces itself if not known by the gateway", func(t *testing.T) {
		transport := localtransport.New()
		s := zephyr.NewService("testService", transport, nil)

		err := s.Start()
		assert.NoError(t, err)

		var serviceDescriptor *zephyr.ServiceDescriptor
		err = transport.BindServiceAnnounce(func(d *zephyr.ServiceDescriptor) {
			serviceDescriptor = d
		})
		assert.NoError(t, err)

		gatewayDescriptor := &zephyr.GatewayDescriptor{
			Name: "testGateway",
		}
		err = transport.AnnounceGateway(gatewayDescriptor)
		assert.NoError(t, err)

		assert.Equal(t, "testService", serviceDescriptor.Name)
	})

	t.Run("Does not re-announce itself if already known by the gateway", func(t *testing.T) {
		transport := localtransport.New()
		s := zephyr.NewService("testService", transport, nil)

		err := s.Start()
		assert.NoError(t, err)

		var serviceDescriptor *zephyr.ServiceDescriptor
		err = transport.BindServiceAnnounce(func(d *zephyr.ServiceDescriptor) {
			serviceDescriptor = d
		})
		assert.NoError(t, err)

		gatewayDescriptor := &zephyr.GatewayDescriptor{
			Name: "testGateway",
			ServiceDescriptors: []*zephyr.ServiceDescriptor{
				{
					Name: "testService",
				},
			},
		}
		err = transport.AnnounceGateway(gatewayDescriptor)
		assert.NoError(t, err)

		assert.Nil(t, serviceDescriptor)
	})
}
