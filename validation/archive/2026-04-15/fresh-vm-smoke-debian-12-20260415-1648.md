# fresh-VM install smoke — debian-12

- Generated: 2026-04-15T16:48:01Z
- Image: `images:debian/12`
- Instance: `ventd-smoke-debian-12-125928-20260415t164744z`
- Binary: `v0.1.1-49-g3c28c4f-dirty`
- Host: `Linux 6.17.0-20-generic x86_64` on bridge `incusbr0` (`10.221.43.1`)

## Assertions

PASS	A1	install.sh exit 0
PASS	A2	systemctl is-active ventd
PASS	A3	curl -k https://127.0.0.1:9999/api/ping → 200
PASS	A4	/api/auth/state → first_boot:true
PASS	A5	setup token present in journal
PASS	A6	/etc/ventd owner=ventd:ventd mode=0750
PASS	A7	no fatal lines in last 2 min
PASS	A8	ventd process owned by user 'ventd'
PASS	A9	uninstall leaves no on-disk orphans

## Summary

- PASS: 9
- FAIL: 0
- SKIP: 0

## Overall: PASS
