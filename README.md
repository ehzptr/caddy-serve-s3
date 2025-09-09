# MinIO Static HTML Handler for Caddy

`caddy-serve-s3` is a [Caddy v2](https://caddyserver.com/) module that serves static HTML files directly from a [MinIO](https://min.io/) bucket.

It includes optional caching with [DragonflyDB](https://www.dragonflydb.io/) or [Redis](https://redis.io/), supporting TTLs and maximum object sizes, so you can reduce repeated S3 requests and improve response latency.

---

## ✨ Features

- 🔗 Serve static HTML directly from MinIO buckets
- ⚡ Optional caching in Redis/DragonflyDB
- ⏳ Per-route or global cache TTLs (`cache_ttl`, `default_cache_ttl`)
- 📦 Configurable maximum cache size (`max_cache_size`)
- 🗂 Custom fallback file for `404 Not Found` cases
- 🛠 Easy configuration via Caddyfile or JSON

---

## 📦 Installation

You need a custom build of Caddy with this module:

```bash
xcaddy build \
  --with github.com/ehzptr/caddy-serve-s3
````

Replace `github.com/ehzptr/caddy-serve-s3` with your repo location.

---

## 🚀 Quick Start with Docker

Here’s a minimal setup using Docker Compose: MinIO + Redis + Caddy with this handler.

```yaml
services:
  minio:
    image: minio/minio:latest
    container_name: minio
    command: server /data --console-address ":9001"
    ports:
      - "9000:9000"
      - "9001:9001"
    environment:
      MINIO_ROOT_USER: minioadmin
      MINIO_ROOT_PASSWORD: minioadmin
    volumes:
      - ./data:/data

  redis:
    image: redis:7
    container_name: redis
    ports:
      - "6379:6379"

  caddy:
    image: caddy:2
    container_name: caddy
    build:
      context: .
      dockerfile: Dockerfile.caddy
    ports:
      - "8080:8080"
    volumes:
      - ./Caddyfile:/etc/caddy/Caddyfile:ro
      - ./errors:/srv/errors
```

### Example `Caddyfile`

```caddyfile
{
  apps {
    minio.config myminio {
      endpoint        "minio:9000"
      access_key      "minioadmin"
      secret_key      "minioadmin"
      secure          false
      reddis_address  "redis://redis:6379/0"
      not_found_file  "/srv/errors/404.html"
      default_cache_ttl "5m"
      max_cache_size  "10MB"
    }
  }
}

:8080 {
  route {
    minio_static_html {
      bucket    "mybucket"
      html_file "index"    # will resolve to index.html
      cache_ttl "2m"
    }
  }
}
```

Put a `404.html` file inside `./errors` to customize error pages.

---

## ⚙️ Configuration

### Global `minio.config`

Global configuration tells the module how to connect to MinIO and Redis/DragonflyDB.

| Option              | Description                                                |
| ------------------- | ---------------------------------------------------------- |
| `endpoint`          | MinIO server endpoint (`host:port`)                        |
| `access_key`        | MinIO access key                                           |
| `secret_key`        | MinIO secret key                                           |
| `secure`            | Use TLS (true/false)                                       |
| `reddis_address`    | Redis/DragonflyDB connection URL (`redis://host:port/db`)  |
| `not_found_file`    | Local file to serve for 404s                               |
| `default_cache_ttl` | Default cache TTL duration (`30s`, `5m`, `1h`, etc.)       |
| `max_cache_size`    | Maximum cacheable object size (`1MB`, `5MB`, `10MB`, etc.) |

---

### Handler `minio_static_html`

Route-level handler configuration.

| Option        | Description                                                                |
| ------------- | -------------------------------------------------------------------------- |
| `bucket`      | The MinIO bucket to serve from (required)                                  |
| `path_prefix` | Strip this prefix from incoming request paths before lookup                |
| `html_file`   | The base name of the `.html` file to serve (e.g. `"index"` → `index.html`) |
| `cache_ttl`   | Override global TTL for this route                                         |

---

## 🔄 JSON Configuration

Example JSON config equivalent to the above:

```json
{
  "apps": {
    "minio.config": {
      "endpoint": "minio:9000",
      "access_key": "minioadmin",
      "secret_key": "minioadmin",
      "secure": false,
      "reddis_address": "redis://redis:6379/0",
      "not_found_file": "/srv/errors/404.html",
      "default_cache_ttl": "5m",
      "max_cache_size": "10485760"
    },
    "http": {
      "servers": {
        "example": {
          "listen": [":8080"],
          "routes": [
            {
              "handle": [
                {
                  "handler": "minio_static_html",
                  "bucket": "mybucket",
                  "html_file": "index",
                  "cache_ttl": "2m"
                }
              ]
            }
          ]
        }
      }
    }
  }
}
```

---

## 🧠 Cache Behavior

* Objects cached in Redis with key format:

  ```
  minio-cache:<bucket>:<objectKey>
  ```
* Cache entries include metadata (Content-Type, ETag, Last-Modified, Size).
* `Cache-Control` headers are set with the TTL.
* Large objects over `max_cache_size` are **not cached**.
* Response headers:

  * `X-Cache-Status: HIT` → Served from cache
  * `X-Cache-Status: MISS` → Fetched from MinIO

---

## 🚨 Error Handling

* **Missing object (`NoSuchKey`)**

  * Serve `not_found_file` if configured
  * Otherwise return HTTP 404
* **Other errors**

  * Log the error
  * Respond with HTTP 500

---

## 🛠 Developer Notes

* Implements these Caddy module interfaces:

  * `http.handlers.minio_static_html`
  * `minio.config`
* Backed by:

  * [minio-go v7](https://github.com/minio/minio-go) (S3 client)
  * [go-redis v9](https://github.com/redis/go-redis) (cache)
* Compatible with [DragonflyDB](https://www.dragonflydb.io/) since it speaks the Redis protocol.

---

## 📜 License

MIT
