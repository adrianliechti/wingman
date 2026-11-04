package google_test

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json/v2"
	"encoding/pem"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/adrianliechti/wingman/pkg/translator"
	"github.com/adrianliechti/wingman/pkg/translator/google"
)

func TestServiceAccountFileTranslation(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	privateKey := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
	credentialJSON, err := json.Marshal(map[string]string{
		"type":           "service_account",
		"project_id":     "service-account-project",
		"private_key_id": "test-key-id",
		"private_key":    string(privateKey),
		"client_email":   "wingman@service-account-project.iam.gserviceaccount.com",
		"token_uri":      "https://oauth2.googleapis.com/token",
	})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "service-account.json")
	if err := os.WriteFile(path, credentialJSON, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GOOGLE_APPLICATION_CREDENTIALS", filepath.Join(t.TempDir(), "unused-adc.json"))
	t.Setenv("GOOGLE_CLOUD_PROJECT", "")
	t.Setenv("GCLOUD_PROJECT", "")
	t.Setenv("GOOGLE_CLOUD_QUOTA_PROJECT", "")

	tokenRequests := 0
	textRequests := 0
	documentRequests := 0
	client, err := google.New("", google.WithToken(path), google.WithClient(&http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		body := ""
		switch r.URL.Host {
		case "oauth2.googleapis.com":
			tokenRequests++
			if err := r.ParseForm(); err != nil {
				t.Errorf("parse OAuth assertion: %v", err)
			}
			if r.Form.Get("grant_type") != "urn:ietf:params:oauth:grant-type:jwt-bearer" {
				t.Error("service account must authenticate using a JWT assertion")
			}
			parts := strings.Split(r.Form.Get("assertion"), ".")
			if len(parts) != 3 {
				t.Errorf("invalid OAuth assertion: %d JWT segments", len(parts))
				return nil, io.ErrUnexpectedEOF
			}
			signature, err := base64.RawURLEncoding.DecodeString(parts[2])
			if err != nil {
				t.Errorf("decode assertion signature: %v", err)
			}
			digest := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
			if err := rsa.VerifyPKCS1v15(&key.PublicKey, crypto.SHA256, digest[:], signature); err != nil {
				t.Errorf("service account assertion signature: %v", err)
			}
			payload, err := base64.RawURLEncoding.DecodeString(parts[1])
			if err != nil {
				t.Errorf("decode assertion claims: %v", err)
			}
			var claims struct {
				Issuer   string `json:"iss"`
				Audience string `json:"aud"`
				Scope    string `json:"scope"`
			}
			if err := json.Unmarshal(payload, &claims); err != nil {
				t.Errorf("decode assertion claims: %v", err)
			}
			if claims.Issuer != "wingman@service-account-project.iam.gserviceaccount.com" || claims.Audience != "https://oauth2.googleapis.com/token" || claims.Scope != "https://www.googleapis.com/auth/cloud-platform" {
				t.Errorf("service account assertion claims = %+v", claims)
			}
			body = `{"access_token":"service-account-token","token_type":"Bearer","expires_in":3600}`
		case "translate.googleapis.com":
			if r.Header.Get("Authorization") != "Bearer service-account-token" || r.Header.Get("X-Goog-Api-Key") != "" {
				t.Error("translation must use the service account OAuth token")
			}
			switch r.URL.Path {
			case "/v3/projects/service-account-project/locations/global:translateText":
				textRequests++
				body = `{"translations":[{"translatedText":"Hallo Welt"}]}`
			case "/v3/projects/service-account-project/locations/global:translateDocument":
				documentRequests++
				body = `{"documentTranslation":{"byteStreamOutputs":["JVBERi10cmFuc2xhdGVk"],"mimeType":"application/pdf"}}`
			default:
				t.Errorf("unexpected translation path: %s", r.URL.Path)
			}
		default:
			t.Errorf("unexpected host: %s", r.URL.Host)
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(body)),
		}, nil
	})}))
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	options := &translator.TranslateOptions{Language: "de"}
	text, err := client.Translate(context.Background(), translator.Input{Text: "Hello world"}, options)
	if err != nil {
		t.Fatal(err)
	}
	if string(text.Content) != "Hallo Welt" {
		t.Errorf("service account text result = %q", text.Content)
	}
	document, err := client.Translate(context.Background(), translator.Input{File: &translator.File{Name: "file.pdf", Content: []byte("%PDF-input")}}, options)
	if err != nil {
		t.Fatal(err)
	}
	if string(document.Content) != "%PDF-translated" || document.Name != "file.pdf" || document.ContentType != "application/pdf" {
		t.Errorf("service account document result = %+v", document)
	}
	if tokenRequests != 1 || textRequests != 1 || documentRequests != 1 {
		t.Errorf("OAuth, text, document requests = %d, %d, %d; want 1, 1, 1", tokenRequests, textRequests, documentRequests)
	}
}

func TestCredentialFileErrors(t *testing.T) {
	dir := t.TempDir()
	for _, tt := range []struct {
		name string
		body string
		want string
	}{
		{name: "invalid.json", body: "invalid", want: "load service account"},
		{name: "user.json", body: `{"type":"authorized_user","client_id":"test","client_secret":"test","refresh_token":"test"}`, want: "load service account"},
		{name: "missing-key.json", body: `{"type":"service_account","client_email":"test@example.com"}`, want: "load service account"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(dir, tt.name)
			if err := os.WriteFile(path, []byte(tt.body), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := google.New("", google.WithToken(path)); err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("New error = %v, want %q", err, tt.want)
			}
		})
	}
	if _, err := google.New("", google.WithToken(filepath.Join(dir, "missing.json"))); err == nil || !strings.Contains(err.Error(), "open credential file") {
		t.Errorf("missing credential file error = %v", err)
	}
	if _, err := google.New("", google.WithToken(dir)); err == nil || !strings.Contains(err.Error(), "not a regular file") {
		t.Errorf("credential directory error = %v", err)
	}
}
