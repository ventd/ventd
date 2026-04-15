# fresh-VM install smoke — ubuntu-24.04

- Generated: 2026-04-15T16:27:15Z
- Image: `images:ubuntu/24.04`
- Instance: `ventd-smoke-ubuntu-24-04-75281-20260415t162714z`
- Binary: `v0.1.1-44-g6ec768b-dirty`
- Host: `Linux 6.17.0-20-generic x86_64` on bridge `incusbr0` (`10.221.43.1`)

## Assertions

PASS	A1	install.sh exit 0
PASS	A2	systemctl is-active ventd
FAIL	A3	api/ping never returned 200 within 60s
```
*   Trying 127.0.0.1:9999...
* Connected to 127.0.0.1 (127.0.0.1) port 9999
> GET /api/ping HTTP/1.1
> Host: 127.0.0.1:9999
> User-Agent: curl/8.5.0
> Accept: */*
> 
* HTTP 1.0, assume close after body
< HTTP/1.0 400 Bad Request
< 
{ [48 bytes data]
* Closing connection
Client sent an HTTP request to an HTTPS server.
```
PASS	A4	setup token present in journal
PASS	A5	/etc/ventd/config.yaml ventd:ventd 0600
FAIL	A6	ventd process user: '' (expected 'ventd')
```
    PID USER     COMMAND
```
PASS	A7	uninstall leaves no on-disk orphans

## Summary

- PASS: 5
- FAIL: 2
- SKIP: 0

## Overall: FAIL
