package tunnel_client

import (
	"bufio"
	"bytes"
	"errors"
	"github.com/iwind/TeaGo/logs"
	"io"
	"io/ioutil"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"runtime"
	"sync"
	"time"
)

// tunnel definition
type Tunnel struct {
	config *TunnelConfig

	conns      []net.Conn
	connLocker sync.Mutex
}

func NewTunnel(config *TunnelConfig) *Tunnel {
	return &Tunnel{
		config: config,
	}
}

func (this *Tunnel) Start() error {
	host := this.config.LocalHost()
	scheme := this.config.LocalScheme()

	if len(host) == 0 {
		return errors.New("local host should not be empty")
	}

	if len(scheme) == 0 {
		scheme = "http"
	}

	for {
		this.connLocker.Lock()
		if len(this.conns) >= runtime.NumCPU()*2 {
			this.connLocker.Unlock()
			time.Sleep(1 * time.Second)
			continue
		}
		this.connLocker.Unlock()

		conn, err := net.Dial("tcp", this.config.Remote)
		if err != nil {
			logs.Println("[error]" + err.Error())
			time.Sleep(10 * time.Second)
			continue
		}

		this.connLocker.Lock()
		this.conns = append(this.conns, conn)
		this.connLocker.Unlock()

		go func(conn net.Conn) {
			if len(this.config.Secret) > 0 {
				conn.Write([]byte(this.config.Secret + "\n"))
			}

			reader := bufio.NewReader(conn)
			for {
				req, err := http.ReadRequest(reader)
				if err != nil {
					if err != io.EOF {
						log.Println("[error]" + err.Error())
					}
					this.connLocker.Lock()
					result := []net.Conn{}
					for _, c := range this.conns {
						if c == conn {
							continue
						}
						result = []net.Conn{}
					}
					this.conns = result
					this.connLocker.Unlock()
					break
				}

				// special urls
				if len(req.Host) == 0 {
					if req.URL.Path == "/$$TEA/ping" { // ping
						body := []byte("OK")
						resp := &http.Response{
							StatusCode:    http.StatusOK,
							Status:        "Ok",
							Proto:         "HTTP/1.1",
							ProtoMajor:    1,
							ProtoMinor:    1,
							ContentLength: int64(len(body)),
							Body:          ioutil.NopCloser(bytes.NewBuffer(body)),
						}
						data, err := httputil.DumpResponse(resp, true)
						if err != nil {
							logs.Error(err)
						} else {
							_, err = conn.Write(data)
							if err != nil {
								logs.Error(err)
							}
						}
						resp.Body.Close()
						continue
					}
				}

				req.RequestURI = ""
				req.URL.Host = host
				req.URL.Scheme = scheme

				logs.Println(req.Header.Get("X-Forwarded-For") + " - \"" + req.Method + " " + req.URL.String() + "\" \"" + req.Header.Get("User-Agent") + "\"")

				if len(this.config.Host) > 0 {
					req.Host = this.config.Host
				} else {
					forwardedHost := req.Header.Get("X-Forwarded-Host")
					if len(forwardedHost) > 0 {
						req.Host = forwardedHost
					} else {
						req.Host = host
					}
				}

				resp, err := HttpClient.Do(req)
				if err != nil {
					logs.Error(err)
					resp := &http.Response{
						StatusCode: http.StatusBadGateway,
						Status:     "Bad Gateway",
						Header: map[string][]string{
							"Content-Type": {"text/plain"},
							"Connection":   {"keep-alive"},
						},
						Proto:      "HTTP/1.1",
						ProtoMajor: 1,
						ProtoMinor: 1,
					}
					data, err := httputil.DumpResponse(resp, false)
					if err != nil {
						logs.Error(err)
						continue
					}
					conn.Write(data)
				} else {
					resp.Header.Set("Connection", "keep-alive")
					data, err := httputil.DumpResponse(resp, true)
					if err != nil {
						logs.Error(err)
						continue
					}
					conn.Write(data)
					resp.Body.Close()
				}
			}
		}(conn)
	}
	return nil
}
