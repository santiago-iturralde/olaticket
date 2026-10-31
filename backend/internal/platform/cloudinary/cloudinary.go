package cloudinary

import (
	"bytes"
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"
)

const defaultAPIURL = "https://api.cloudinary.com"

// Folder es la carpeta de Cloudinary donde quedan las imágenes de los eventos.
const Folder = "olaticket/eventos"

// Config sale de CLOUDINARY_URL ("cloudinary://<api_key>:<api_secret>@<cloud_name>"),
// el formato que muestra el dashboard de Cloudinary.
type Config struct {
	CloudName string
	APIKey    string
	APISecret string
}

// ParseURL interpreta una CLOUDINARY_URL. Vacía = sin Cloudinary (Config cero, sin error).
func ParseURL(raw string) (Config, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return Config{}, nil
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "cloudinary" || u.User == nil {
		return Config{}, errors.New("CLOUDINARY_URL debe tener el formato cloudinary://<api_key>:<api_secret>@<cloud_name>")
	}
	secret, _ := u.User.Password()
	cfg := Config{CloudName: u.Host, APIKey: u.User.Username(), APISecret: secret}
	if cfg.CloudName == "" || cfg.APIKey == "" || cfg.APISecret == "" {
		return Config{}, errors.New("CLOUDINARY_URL incompleta: faltan api_key, api_secret o cloud_name")
	}
	return cfg, nil
}

func ConfigFromEnv() (Config, error) {
	return ParseURL(os.Getenv("CLOUDINARY_URL"))
}

// Uploader sube imágenes con la Upload API firmada (POST /v1_1/<cloud>/image/upload).
type Uploader struct {
	cfg    Config
	apiURL string
	client *http.Client
	now    func() time.Time
}

func NewUploader(cfg Config) *Uploader {
	return &Uploader{
		cfg:    cfg,
		apiURL: defaultAPIURL,
		client: &http.Client{Timeout: 30 * time.Second},
		now:    time.Now,
	}
}

// Enabled indica si hay credenciales: sin CLOUDINARY_URL no se pueden subir imágenes.
func (u *Uploader) Enabled() bool {
	return u != nil && u.cfg.CloudName != ""
}

// sign firma los parámetros como pide Cloudinary: ordenados alfabéticamente,
// "clave=valor" unidos con "&", seguidos del api_secret, en SHA-1 hex.
func (u *Uploader) sign(params map[string]string) string {
	keys := make([]string, 0, len(params))
	for k := range params {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	pairs := make([]string, len(keys))
	for i, k := range keys {
		pairs[i] = k + "=" + params[k]
	}
	sum := sha1.Sum([]byte(strings.Join(pairs, "&") + u.cfg.APISecret))
	return hex.EncodeToString(sum[:])
}

type uploadResponse struct {
	SecureURL string `json:"secure_url"`
	Error     *struct {
		Message string `json:"message"`
	} `json:"error"`
}

// Upload sube la imagen a Folder y devuelve su URL pública HTTPS.
func (u *Uploader) Upload(ctx context.Context, file io.Reader, filename string) (string, error) {
	if !u.Enabled() {
		return "", errors.New("cloudinary no configurado")
	}
	params := map[string]string{
		"folder":    Folder,
		"timestamp": strconv.FormatInt(u.now().Unix(), 10),
	}

	var body bytes.Buffer
	form := multipart.NewWriter(&body)
	for k, v := range params {
		if err := form.WriteField(k, v); err != nil {
			return "", err
		}
	}
	if err := form.WriteField("api_key", u.cfg.APIKey); err != nil {
		return "", err
	}
	if err := form.WriteField("signature", u.sign(params)); err != nil {
		return "", err
	}
	part, err := form.CreateFormFile("file", filename)
	if err != nil {
		return "", err
	}
	if _, err := io.Copy(part, file); err != nil {
		return "", fmt.Errorf("error leyendo la imagen: %w", err)
	}
	if err := form.Close(); err != nil {
		return "", err
	}

	endpoint := fmt.Sprintf("%s/v1_1/%s/image/upload", u.apiURL, url.PathEscape(u.cfg.CloudName))
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, &body)
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", form.FormDataContentType())

	res, err := u.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("error conectando con Cloudinary: %w", err)
	}
	defer res.Body.Close()

	var out uploadResponse
	if err := json.NewDecoder(io.LimitReader(res.Body, 1<<20)).Decode(&out); err != nil {
		return "", fmt.Errorf("cloudinary respondió %d con un cuerpo inválido: %w", res.StatusCode, err)
	}
	if res.StatusCode < 200 || res.StatusCode >= 300 || out.SecureURL == "" {
		msg := "sin detalle"
		if out.Error != nil {
			msg = out.Error.Message
		}
		return "", fmt.Errorf("cloudinary respondió %d: %s", res.StatusCode, msg)
	}
	return out.SecureURL, nil
}
