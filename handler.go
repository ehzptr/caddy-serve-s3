package miniohandler

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/caddyserver/caddy/v2"
	"github.com/caddyserver/caddy/v2/modules/caddyhttp"
	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"
)

// Register the modules with Caddy.
func init() {
	caddy.RegisterModule(MinioStaticHTML{})
	caddy.RegisterModule(MinioConfigModule{})
}

// MinioStaticHTML is a Caddy HTTP handler that serves files from a MinIO bucket.
type MinioStaticHTML struct {
	// The MinIO bucket to serve files from. (Required)
	Bucket string `json:"bucket,omitempty"`

	// An optional path prefix to strip from the request URI before looking
	// up the object in the bucket.
	PathPrefix string `json:"path_prefix,omitempty"`

	// The duration for which to cache objects in DragonflyDB/Redis.
	// This overrides the global `default_cache_ttl`.
	// Examples: "1h", "30m", "5m30s". If empty, the global default is used.
	CacheTTL string `json:"cache_ttl,omitempty"`

	HtmlFile string `json:"html_file,omitempty"`

	client          *minio.Client
	logger          *zap.Logger
	dragonflyClient *redis.Client
	cacheTTL        time.Duration
	globalConfig    *MinioConfigModule
}

// CachedObject defines the structure for storing objects in the cache.
type CachedObject struct {
	ContentType  string
	ETag         string
	LastModified time.Time
	Size         int64
	Content      []byte
}

// CaddyModule returns the Caddy module information for the handler.
func (MinioStaticHTML) CaddyModule() caddy.ModuleInfo {
	return caddy.ModuleInfo{
		ID:  "http.handlers.minio_static_html",
		New: func() caddy.Module { return new(MinioStaticHTML) },
	}
}

// Provision sets up the MinioStaticHTML module.
func (h *MinioStaticHTML) Provision(ctx caddy.Context) error {
	h.logger = ctx.Logger()

	// Load the shared global MinIO & DragonflyDB configuration
	val, err := ctx.App("minio_static_html.config")
	if err != nil {
		return fmt.Errorf("the 'minio_static_html.config' app is not loaded; please configure it globally")
	}
	cfg := val.(*MinioConfigModule)
	h.globalConfig = cfg // Store a reference to the global config

	if h.Bucket == "" {
		return fmt.Errorf("bucket must be specified")
	}

	// Initialize the MinIO client using the global configuration.
	client, err := minio.New(cfg.Endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(cfg.AccessKey, cfg.SecretKey, ""),
		Secure: cfg.Secure,
	})
	if err != nil {
		return fmt.Errorf("failed to initialize MinIO client: %w", err)
	}
	h.client = client

	// Set up DragonflyDB client and parse TTL if configured
	if cfg.DragonflyClient != nil {
		h.dragonflyClient = cfg.DragonflyClient

		// Use per-route TTL if set, otherwise fall back to global default
		ttlToParse := h.CacheTTL
		if ttlToParse == "" {
			ttlToParse = cfg.DefaultCacheTTL
		}

		if ttlToParse != "" {
			dur, err := time.ParseDuration(ttlToParse)
			if err != nil {
				h.logger.Warn("invalid cache_ttl duration; caching will be disabled",
					zap.String("ttl", ttlToParse),
					zap.Error(err),
				)
			} else {
				h.cacheTTL = dur
			}
		}
	}

	h.logger.Info("provisioned minio file server",
		zap.String("bucket", h.Bucket),
		zap.String("path_prefix", h.PathPrefix),
		zap.Bool("caching_enabled", h.cacheTTL > 0),
		zap.Duration("cache_ttl", h.cacheTTL),
	)

	return nil
}

// ServeHTTP handles the HTTP request by fetching from cache or MinIO.
func (h *MinioStaticHTML) ServeHTTP(w http.ResponseWriter, r *http.Request, next caddyhttp.Handler) error {
	if strings.Contains(r.URL.Path, "..") {
		return caddyhttp.Error(http.StatusBadRequest, errors.New("invalid URL path"))
	}

	objectKey := fmt.Sprintf("%s.html", h.HtmlFile)

	// objectKey := strings.TrimPrefix(r.URL.Path, h.PathPrefix)
	// objectKey = strings.TrimPrefix(objectKey, "/")
	// if strings.HasSuffix(objectKey, "/") || objectKey == "" {
	// 	objectKey += "index.html"
	// }

	// 1. Try to serve from cache
	if h.dragonflyClient != nil && h.cacheTTL > 0 {
		cacheKey := fmt.Sprintf("minio-cache:%s:%s", h.Bucket, objectKey)
		cachedResult, err := h.dragonflyClient.Get(r.Context(), cacheKey).Result()
		if err == nil {
			var cachedObj CachedObject
			if err := json.Unmarshal([]byte(cachedResult), &cachedObj); err == nil {
				h.logger.Debug("cache hit", zap.String("key", cacheKey))
				h.serveFromCache(w, r, &cachedObj)
				return nil // Request handled
			}
			h.logger.Warn("failed to unmarshal cached object", zap.String("key", cacheKey), zap.Error(err))
		} else if err != redis.Nil {
			h.logger.Error("dragonflyDB GET error", zap.String("key", cacheKey), zap.Error(err))
		}
	}

	// 2. Cache MISS: Fetch from MinIO
	h.logger.Debug("cache miss, fetching from minio",
		zap.String("bucket", h.Bucket),
		zap.String("object_key", objectKey),
	)

	objInfo, err := h.client.StatObject(r.Context(), h.Bucket, objectKey, minio.StatObjectOptions{})
	if err != nil {
		h.handleMinioError(w, r, err)
		return nil
	}

	obj, err := h.client.GetObject(r.Context(), h.Bucket, objectKey, minio.GetObjectOptions{})
	if err != nil {
		h.handleMinioError(w, r, err)
		return nil
	}
	defer obj.Close()

	content, err := io.ReadAll(obj)
	if err != nil {
		h.logger.Error("failed to read object content from minio", zap.Error(err))
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return nil
	}

	// 3. Store in cache
	if h.dragonflyClient != nil && h.cacheTTL > 0 {
		cacheKey := fmt.Sprintf("minio-cache:%s:%s", h.Bucket, objectKey)
		cachedObj := CachedObject{
			ContentType:  objInfo.ContentType,
			ETag:         objInfo.ETag,
			LastModified: objInfo.LastModified,
			Size:         objInfo.Size,
			Content:      content,
		}
		jsonData, err := json.Marshal(cachedObj)
		if err != nil {
			h.logger.Error("failed to marshal object for caching", zap.Error(err))
		} else {
			err := h.dragonflyClient.Set(r.Context(), cacheKey, jsonData, h.cacheTTL).Err()
			if err != nil {
				h.logger.Error("failed to SET object in cache", zap.String("key", cacheKey), zap.Error(err))
			} else {
				h.logger.Debug("stored object in cache", zap.String("key", cacheKey))
			}
		}
	}

	// 4. Serve the object to the client
	h.serveFromOrigin(w, r, &objInfo, content)
	return nil
}

// serveFromCache writes a cached object to the HTTP response.
func (h *MinioStaticHTML) serveFromCache(w http.ResponseWriter, r *http.Request, obj *CachedObject) {
	w.Header().Set("Content-Type", obj.ContentType)
	w.Header().Set("Content-Length", fmt.Sprintf("%d", obj.Size))
	w.Header().Set("ETag", obj.ETag)
	w.Header().Set("Last-Modified", obj.LastModified.Format(http.TimeFormat))
	w.Header().Set("X-Cache-Status", "HIT")
	http.ServeContent(w, r, "", obj.LastModified, bytes.NewReader(obj.Content))
}

// serveFromOrigin writes an object just fetched from MinIO to the response.
func (h *MinioStaticHTML) serveFromOrigin(w http.ResponseWriter, r *http.Request, objInfo *minio.ObjectInfo, content []byte) {
	w.Header().Set("Content-Type", objInfo.ContentType)
	w.Header().Set("Content-Length", fmt.Sprintf("%d", objInfo.Size))
	w.Header().Set("ETag", objInfo.ETag)
	w.Header().Set("Last-Modified", objInfo.LastModified.Format(http.TimeFormat))
	w.Header().Set("X-Cache-Status", "MISS")
	http.ServeContent(w, r, "", objInfo.LastModified, bytes.NewReader(content))
}

func (h *MinioStaticHTML) handleMinioError(w http.ResponseWriter, r *http.Request, err error) {
	minioErr, ok := err.(minio.ErrorResponse)
	if !ok {
		h.logger.Error("unhandled error from minio client", zap.Error(err))
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}
	if minioErr.Code == "NoSuchKey" {
		h.logger.Debug("object not found in bucket", zap.Error(err))
		if h.globalConfig.NotFoundFile != "" {
			http.ServeFile(w, r, h.globalConfig.NotFoundFile)
		} else {
			http.NotFound(w, r)
		}
		return
	}
	h.logger.Error("minio returned an error",
		zap.String("error_code", minioErr.Code),
		zap.String("bucket", minioErr.BucketName),
		zap.String("key", minioErr.Key),
	)
	http.Error(w, "Internal Server Error", http.StatusInternalServerError)
}

// MinioConfigModule is the global app configuration for MinIO and DragonflyDB.
type MinioConfigModule struct {
	Endpoint         string `json:"endpoint,omitempty"`
	AccessKey        string `json:"access_key,omitempty"`
	SecretKey        string `json:"secret_key,omitempty"`
	Secure           bool   `json:"secure,omitempty"`
	DragonflyAddress string `json:"dragonfly_address,omitempty"`
	NotFoundFile     string `json:"not_found_file,omitempty"`
	DefaultCacheTTL  string `json:"default_cache_ttl,omitempty"`

	DragonflyClient *redis.Client `json:"-"`
}

func (MinioConfigModule) CaddyModule() caddy.ModuleInfo {
	return caddy.ModuleInfo{
		ID:  "minio_static_html.config",
		New: func() caddy.Module { return new(MinioConfigModule) },
	}
}

// Provision initializes the DragonflyDB/Redis client.
func (m *MinioConfigModule) Provision(ctx caddy.Context) error {
	if m.DragonflyAddress != "" {
		opt, err := redis.ParseURL(m.DragonflyAddress)
		if err != nil {
			return fmt.Errorf("invalid dragonfly_address URL: %w", err)
		}
		client := redis.NewClient(opt)
		if err := client.Ping(context.Background()).Err(); err != nil {
			return fmt.Errorf("failed to connect to dragonflyDB at %s: %w", m.DragonflyAddress, err)
		}
		m.DragonflyClient = client
		ctx.Logger().Info("connected to dragonflyDB", zap.String("address", m.DragonflyAddress))
	}
	return nil
}

func (m *MinioConfigModule) Start() error { return nil }

// Stop satisfies the caddy.App interface. It currently does nothing.
func (m *MinioConfigModule) Stop() error { return nil }

// Cleanup closes the DragonflyDB/Redis client connection.
func (m *MinioConfigModule) Cleanup() error {
	if m.DragonflyClient != nil {
		return m.DragonflyClient.Close()
	}
	return nil
}

// func (m *MinioConfigModule) UnmarshalCaddyfile(d *caddyfile.Dispenser) error {
// 	for d.Next() {
// 		if !d.NextArg() {
// 			return d.ArgErr()
// 		}
// 		val := d.Val()
// 		for d.NextBlock(0) {
// 			switch d.Val() {
// 			case "endpoint":
// 				if !d.NextArg() {
// 					return d.ArgErr()
// 				}
// 				m.Endpoint = d.Val()
// 			case "access_key":
// 				if !d.NextArg() {
// 					return d.ArgErr()
// 				}
// 				m.AccessKey = d.Val()
// 			case "secret_key":
// 				if !d.NextArg() {
// 					return d.ArgErr()
// 				}
// 				m.SecretKey = d.Val()
// 			case "secure":
// 				if !d.NextArg() {
// 					return d.ArgErr()
// 				}
// 				m.Secure = (d.Val() == "true")
// 			case "dragonfly_address":
// 				if !d.NextArg() {
// 					return d.ArgErr()
// 				}
// 				m.DragonflyAddress = d.Val()
// 			case "not_found_file":
// 				if !d.NextArg() {
// 					return d.ArgErr()
// 				}
// 				m.NotFoundFile = d.Val()
// 			case "default_cache_ttl":
// 				if !d.NextArg() {
// 					return d.ArgErr()
// 				}
// 				m.DefaultCacheTTL = d.Val()
// 			default:
// 				return d.Errf("unrecognized subdirective '%s'", d.Val())
// 			}
// 		}
// 		if m.Endpoint == "" {
// 			m.Endpoint = val
// 		}
// 	}
// 	return nil
// }

var (
	_ caddyhttp.MiddlewareHandler = (*MinioStaticHTML)(nil)
	_ caddy.App                   = (*MinioConfigModule)(nil)
	// _ caddyfile.Unmarshaler       = (*MinioConfigModule)(nil)
	_ caddy.Provisioner  = (*MinioConfigModule)(nil)
	_ caddy.CleanerUpper = (*MinioConfigModule)(nil)
)
