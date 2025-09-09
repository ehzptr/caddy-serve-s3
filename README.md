# 🪣 MinIO Static HTML Handler for Caddy v2

**caddy-serve-s3** is a custom Caddy v2 HTTP module that serves static HTML files directly from a **MinIO** (or any S3-compatible) object storage bucket. It optionally supports **caching** via **DragonflyDB** or **Redis** for improved performance.

> This module is ideal for serving static frontends, single-page applications (SPAs), or HTML-based content directly from object storage with optional caching for low-latency delivery.

## 🚀 Features

* 🔒 Secure integration with **MinIO** or any S3-compatible storage.
* ⚡ Optional caching using **DragonflyDB** or **Redis**.
* 🔥 Cache TTL control per route or globally.
* 📂 Configurable fallback for `404 Not Found`.
* 🧩 Pluggable into Caddy’s modular architecture via JSON or Caddyfile.

---

## 📦 Installation

> **Note:** This is a Caddy **custom module**. You'll need to [build Caddy from source](https://caddyserver.com/docs/build#xcaddy) with this module:

```bash
xcaddy build --with github.com/ehzptr/caddy-serve-s3
```

---

## ⚙️ Configuration

### JSON Configuration

```json
{
  "apps": {
    "http": {
      "servers": {
        "example": {
          "listen": [":80"],
          "routes": [
            {
              "handle": [
                {
                  "handler": "minio_static_html",
                  "bucket": "my-bucket",
                  "path_prefix": "/static/",
                  "cache_ttl": "10m",
                  "html_file": "index"
                }
              ]
            }
          ]
        }
      }
    },
    "minio_static_html.config": {
      "endpoint": "play.min.io:9000",
      "access_key": "minioadmin",
      "secret_key": "minioadmin",
      "secure": true,
      "dragonfly_address": "redis://localhost:6379",
      "default_cache_ttl": "5m",
      "not_found_file": "./404.html"
    }
  }
}
```

### Caddyfile Configuration

```caddyfile
{
  order minio_static_html before file_server
}

minio_static_html_config play.min.io:9000 {
  access_key minioadmin
  secret_key minioadmin
  secure true
  dragonfly_address redis://localhost:6379
  default_cache_ttl 5m
  not_found_file ./404.html
}

:80 {
  route /static/* {
    minio_static_html {
      bucket my-bucket
      path_prefix /static/
      cache_ttl 10m
      html_file index
    }
  }
}
```

---

## 🧠 How It Works

1. **Intercepts requests** and builds the object key (e.g., `index.html`).
2. Checks **DragonflyDB/Redis** for a cached version.
3. If **cache hit**, serves the object from cache.
4. On **cache miss**, fetches from **MinIO** and stores it in the cache.
5. Optionally serves a `404.html` file on missing objects.

---

## 🛠️ Development

### Requirements

* Go 1.18+
* Caddy v2
* MinIO/S3-compatible server
* Optional: DragonflyDB or Redis

### Building Locally

```bash
git clone https://github.com/ehzptr/caddy-serve-s3
cd caddy-serve-s3
xcaddy build --with github.com/ehzptr/caddy-serve-s3
```

---

## ❗ Notes

* The `HtmlFile` field determines the file name (without `.html`) that will be requested from the bucket.
* Cache key format: `minio-cache:{bucket}:{object_key}`
* If `dragonfly_address` is omitted, caching is disabled.

---

## 🧪 Example Use Case

Serve a pre-built React/Vue app stored in MinIO, using DragonflyDB to cache `index.html` for 10 minutes, and serve a custom `404.html` if the object is missing.

---

## 👤 Author

Maintained by [ehzptr](https://github.com/ehzptr)

---

## 📝 License

[MIT](./LICENSE)
