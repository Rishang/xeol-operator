# Air-gapped installation

In online mode the operator builds its EOL database from [endoflife.date](https://endoflife.date) at startup and daily. Without egress, build the database on a connected machine and give it to the operator with `db.source=mounted`. The operator then never touches the network except to pull the images it scans from your internal registry.

## 1. Build the database (connected machine)

The operator image can build it:

```bash
docker run --rm -v "$PWD/db:/out" --entrypoint /manager rishang/xeol-operator:<version> build-db --out /out
# -> db/1/xeol.db, db/1/metadata.json
```

Or from a saved endoflife.date dump, e.g. one fetched through a proxy:

```bash
curl -sSfL https://endoflife.date/api/v1/products/full -o products-full.json
docker run --rm -v "$PWD:/work" --entrypoint /manager rishang/xeol-operator:<version> \
  build-db --from /work/products-full.json --out /work/db
```

Check it with any xeol v0.10.8 binary: `XEOL_DB_CACHE_DIR=./db xeol db status` should print `Status: valid`.

Rebuild and redistribute regularly. The `xeol_db_built_timestamp_seconds` metric, the `DBStale` condition on `kubectl get xeolscanner default`, and the `XeolDatabaseStale` alert (30 days by default) tell you when it's due.

## 2. Get it into the cluster

Pick one.

### a. Database image (recommended)

Air-gapped clusters already mirror images, so ship the database as one:

```dockerfile
FROM busybox:1.37
COPY db /db
```

```bash
docker build -t registry.internal/xeol-db:$(date +%F) .
docker push registry.internal/xeol-db:$(date +%F)
```

```yaml
# values.yaml
db:
  source: mounted
  mounted:
    initContainer:
      image: registry.internal/xeol-db:2026-10-26
```

An init container copies `/db` into an `emptyDir` at startup. The image needs `sh` and `cp`; override `db.mounted.initContainer.command` for a different layout. To update: push a new tag, `helm upgrade --set db.mounted.initContainer.image=...`.

### b. Volume (PVC, NFS, hostPath)

Copy `db/` onto any volume and mount it read-only:

```yaml
db:
  source: mounted
  mounted:
    path: /xeol-db
    volume:
      persistentVolumeClaim:
        claimName: xeol-db
```

The volume must contain `1/xeol.db` and `1/metadata.json` at its root. To update, replace the files; the next scan uses them. Restart the operator if you want an immediate rescan of every image.

### c. Internal endoflife.date mirror (online mode, no egress to the internet)

If you can host one file internally, keep online mode and point the operator at it:

```bash
curl -sSfL https://endoflife.date/api/v1/products/full -o products-full.json
# upload to e.g. https://files.internal/eol/products-full.json and refresh it daily
```

```yaml
db:
  source: online
  url: https://files.internal/eol/products-full.json
```

## Registry access

Scans pull images by digest from the registries your Pods use, with the Pods' `imagePullSecrets` (and their ServiceAccounts'). In an air-gapped cluster those are your internal registries, so nothing extra is needed. Images loaded straight onto nodes (`k3d image import`, `kind load`) have no registry digest and are skipped.

## What never leaves the cluster

The operator never contacts `data.xeol.io` or `xeol.io`: xeol runs with auto-update and the update check off, and never with `--api-key`.
