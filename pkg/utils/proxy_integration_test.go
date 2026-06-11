package utils

import (
	"bufio"
	"crypto/tls"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestProxyRoutingEndToEnd prova que o endereco gerado por BuildProxyAddress,
// aplicado da mesma forma que a whatsmeow faz (transport.Proxy = http.ProxyURL),
// realmente roteia o trafego pelo proxy. Isso fecha a hipotese do "grava mas
// nao usa": com a montagem correta, o CONNECT chega ao proxy.
//
// A whatsmeow (client.go SetProxy) executa exatamente:
//
//	transport := http.DefaultTransport.Clone()
//	transport.Proxy = http.ProxyURL(parsed)   // parsed = url.Parse(BuildProxyAddress(...))
//
// e usa esse transport no http.Client do websocket. Reproduzimos esse caminho.
func TestProxyRoutingEndToEnd(t *testing.T) {
	// Destino HTTPS (equivalente ao endpoint do WhatsApp do ponto de vista do tunel TLS).
	backend := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	}))
	defer backend.Close()
	backendURL, _ := url.Parse(backend.URL)

	// Proxy HTTP CONNECT minimo que registra cada destino tunelado.
	var mu sync.Mutex
	var connects []string
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("nao consegui abrir o proxy de teste: %v", err)
	}
	defer ln.Close()

	go func() {
		for {
			client, err := ln.Accept()
			if err != nil {
				return
			}
			go func(client net.Conn) {
				defer client.Close()
				br := bufio.NewReader(client)
				reqLine, err := br.ReadString('\n')
				if err != nil {
					return
				}
				fields := strings.Fields(reqLine)
				if len(fields) < 2 || fields[0] != "CONNECT" {
					_, _ = client.Write([]byte("HTTP/1.1 405 Method Not Allowed\r\n\r\n"))
					return
				}
				target := fields[1]
				mu.Lock()
				connects = append(connects, target)
				mu.Unlock()

				// Consome os headers ate a linha em branco.
				for {
					line, err := br.ReadString('\n')
					if err != nil {
						return
					}
					if line == "\r\n" || line == "\n" {
						break
					}
				}

				dst, err := net.Dial("tcp", target)
				if err != nil {
					_, _ = client.Write([]byte("HTTP/1.1 502 Bad Gateway\r\n\r\n"))
					return
				}
				defer dst.Close()

				if _, err := client.Write([]byte("HTTP/1.1 200 Connection established\r\n\r\n")); err != nil {
					return
				}

				// Tunel bidirecional. Usa o bufio.Reader para nao perder bytes ja bufferizados.
				done := make(chan struct{}, 2)
				go func() { _, _ = io.Copy(dst, br); done <- struct{}{} }()
				go func() { _, _ = io.Copy(client, dst); done <- struct{}{} }()
				<-done
			}(client)
		}
	}()

	proxyHost, proxyPort, _ := net.SplitHostPort(ln.Addr().String())

	addr, err := BuildProxyAddress("http", proxyHost, proxyPort, "", "")
	if err != nil {
		t.Fatalf("BuildProxyAddress falhou: %v", err)
	}
	parsed, err := url.Parse(addr)
	if err != nil {
		t.Fatalf("endereco de proxy invalido: %v", err)
	}

	// Mesmo caminho que whatsmeow.Client.SetProxy executa.
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = http.ProxyURL(parsed)
	transport.TLSClientConfig = &tls.Config{InsecureSkipVerify: true}

	client := &http.Client{Transport: transport, Timeout: 5 * time.Second}

	resp, err := client.Get(backend.URL)
	if err != nil {
		t.Fatalf("requisicao via proxy falhou: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != http.StatusOK || string(body) != "ok" {
		t.Fatalf("resposta inesperada: status=%d body=%q", resp.StatusCode, string(body))
	}

	mu.Lock()
	defer mu.Unlock()
	if len(connects) == 0 {
		t.Fatal("o proxy NAO recebeu nenhum CONNECT — trafego nao passou pelo proxy")
	}
	if connects[0] != backendURL.Host {
		t.Fatalf("CONNECT foi para %q; esperado %q", connects[0], backendURL.Host)
	}
	t.Logf("OK: trafego roteado pelo proxy (CONNECT %s)", connects[0])
}
