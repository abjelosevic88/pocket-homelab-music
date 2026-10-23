# Behind a reverse proxy

Pocket Music is one HTTP service on port 8080. Two kinds of requests are long:

- **audio** (`/api/v1/stream/…`), a song for minutes, often with byte ranges;
- **live updates** (`/api/v1/events`), server-sent events kept open while the app is open.

So the proxy must not buffer responses and must not time them out early. Set `SECURE_COOKIES=true` behind HTTPS
and `BASE_URL` to the public address.

## Caddy

```caddy
music.example.com {
	reverse_proxy pocket-music:8080 {
		flush_interval -1
	}
}
```

## nginx

```nginx
location / {
    proxy_pass http://pocket-music:8080;
    proxy_http_version 1.1;
    proxy_set_header Host $host;
    proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
    proxy_set_header X-Forwarded-Proto $scheme;
    proxy_buffering off;
    proxy_read_timeout 1h;
}
```

## Traefik

Nothing special: Traefik streams by default. Add `traefik.http.services.music.loadbalancer.responseforwarding.flushinterval=-1ms` if events arrive late.

## The music server

Pocket Music talks to Navidrome over the internal network (`NAVIDROME_URL`), so Navidrome doesn't need to be
public for Pocket Music. Keep it reachable for your other Subsonic apps as before.
