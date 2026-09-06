# One distribution, two modules

From this directory, run:

```sh
go run ../../cmd/gobridge build --python --typescript
```

Install the resulting wheel to import `acme.catalog` and `acme.text`. Install the
npm tarball to import `gobridge-multi-example/catalog` and
`gobridge-multi-example/text`. Each module owns its own lazy Go process and can
also create explicit isolated clients.

The catalog exposes Go functional options as constructor keywords:

```python
from acme.catalog import SyncCatalogClient

with SyncCatalogClient(base_url="https://example.test", retries=0) as client:
    print(client.get_status())
```

See [modules and naming](../../README.md#configuration) for per-language rename
maps, annotations, and development commands. Outputs are local; nothing is
published by the build command.
