# gen-msiec-firmware-catalogue

Regenerates `internal/hal/msiec/firmware_catalogue.go` from upstream
`BeardOverflow/msi-ec`'s `msi-ec.c` ALLOWED_FW_G* tables. The
catalogue is consumed by `firmware_diagnose.go` to suggest closest-match
firmware pins (see issue #1168 — the modparam escape hatch for boards
whose firmware isn't in any upstream allowlist yet).

## Regeneration

```
curl -sSL -o /tmp/msi-ec.c \
  https://raw.githubusercontent.com/BeardOverflow/msi-ec/master/msi-ec.c
python3 tools/gen-msiec-firmware-catalogue/extract.py \
  > internal/hal/msiec/firmware_catalogue.go
gofmt -w internal/hal/msiec/firmware_catalogue.go
```

Refresh cadence: bump when upstream msi-ec adds a new CONF_G group or a
user reports a firmware string that ventd's suggestion engine doesn't
catalogue (close: #1168 follow-up tickets).

## Why a script, not a Go //go:generate target

The catalogue can be regenerated offline by anyone who has fetched
msi-ec.c — no network access from `go generate ./...` (CI runners
shouldn't reach github.com mid-test). Run the curl + python by hand
when refreshing; commit the generated file along with the upstream SHA
(noted in the commit message).
