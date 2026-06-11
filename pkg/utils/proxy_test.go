package utils

import (
	"net/url"
	"testing"
)

func TestNormalizeProxyProtocol(t *testing.T) {
	cases := []struct {
		name     string
		protocol string
		port     string
		want     string
	}{
		{"http explicito", "http", "8080", "http"},
		{"https explicito", "https", "443", "https"},
		{"socks5 explicito", "socks5", "1080", "socks5"},
		{"alias socks vira socks5", "socks", "1080", "socks5"},
		{"protocolo vazio porta 1080 -> socks5", "", "1080", "socks5"},
		{"protocolo vazio porta 2080 -> socks5", "", "2080", "socks5"},
		{"protocolo vazio porta 42000 -> socks5", "", "42000", "socks5"},
		{"protocolo vazio porta 42500 -> socks5", "", "42500", "socks5"},
		{"protocolo vazio porta generica -> http", "", "8080", "http"},
		{"protocolo desconhecido cai na inferencia por porta", "qualquer", "1080", "socks5"},
		{"case-insensitive com espacos", "  HTTPS ", "443", "https"},
		// O protocolo explicito sempre vence a inferencia por porta: um proxy HTTP
		// rodando numa porta "de socks" continua sendo http se informado.
		{"http explicito em porta 1080 continua http", "http", "1080", "http"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := NormalizeProxyProtocol(tc.protocol, tc.port); got != tc.want {
				t.Fatalf("NormalizeProxyProtocol(%q, %q) = %q; esperado %q", tc.protocol, tc.port, got, tc.want)
			}
		})
	}
}

func TestBuildProxyAddress(t *testing.T) {
	cases := []struct {
		name                           string
		protocol, host, port, user, pw string
		want                           string
	}{
		{"http com auth", "http", "1.2.3.4", "8080", "user", "pass", "http://user:pass@1.2.3.4:8080"},
		{"http sem auth", "http", "1.2.3.4", "8080", "", "", "http://1.2.3.4:8080"},
		{"socks5 com auth", "socks5", "proxy.local", "1080", "u", "p", "socks5://u:p@proxy.local:1080"},
		{"https sem auth", "https", "h", "443", "", "", "https://h:443"},
		{"so usuario sem senha", "http", "h", "8080", "user", "", "http://user@h:8080"},
		{"protocolo inferido da porta (1080 -> socks5)", "", "h", "1080", "u", "p", "socks5://u:p@h:1080"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := BuildProxyAddress(tc.protocol, tc.host, tc.port, tc.user, tc.pw)
			if err != nil {
				t.Fatalf("erro inesperado: %v", err)
			}
			if got != tc.want {
				t.Fatalf("BuildProxyAddress(%q,%q,%q,%q,%q) = %q; esperado %q",
					tc.protocol, tc.host, tc.port, tc.user, tc.pw, got, tc.want)
			}
		})
	}
}

func TestBuildProxyAddressErros(t *testing.T) {
	if _, err := BuildProxyAddress("http", "", "8080", "", ""); err == nil {
		t.Fatal("esperado erro quando host esta vazio")
	}
	if _, err := BuildProxyAddress("http", "h", "", "", ""); err == nil {
		t.Fatal("esperado erro quando porta esta vazia")
	}
}

// TestBuildProxyAddressCaracteresEspeciais garante que credenciais com caracteres
// reservados de URL (@ : / etc.) sobrevivem ao round-trip — a whatsmeow faz
// url.Parse no endereco retornado, entao ele precisa ser parseavel e preservar
// usuario/senha. Antes da correcao, a montagem manual com fmt.Sprintf quebrava aqui.
func TestBuildProxyAddressCaracteresEspeciais(t *testing.T) {
	const user = "user"
	const senha = "p@ss/w:rd#1"

	addr, err := BuildProxyAddress("http", "proxy.local", "8080", user, senha)
	if err != nil {
		t.Fatalf("erro inesperado: %v", err)
	}

	u, err := url.Parse(addr)
	if err != nil {
		t.Fatalf("endereco gerado nao e parseavel (%q): %v", addr, err)
	}

	if u.Scheme != "http" {
		t.Fatalf("scheme = %q; esperado http", u.Scheme)
	}
	if u.Hostname() != "proxy.local" || u.Port() != "8080" {
		t.Fatalf("host:port = %q:%q; esperado proxy.local:8080", u.Hostname(), u.Port())
	}
	if u.User.Username() != user {
		t.Fatalf("usuario = %q; esperado %q", u.User.Username(), user)
	}
	if pw, _ := u.User.Password(); pw != senha {
		t.Fatalf("senha apos round-trip = %q; esperado %q", pw, senha)
	}
}
