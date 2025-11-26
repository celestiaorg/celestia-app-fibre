# celestia-app-fibre

celestia-app-fibre is a fork of [celestia-app](https://github.com/celestiaorg/celestia-app) that adds support for Fibre DA.

The canonical branch in this repo is `feature/fibre`.

This repo is temporary and will eventually be merged into celestia-app.

## Contributing

Since this is a private repo, CI won't run on PRs made from forks. Please push to branches on the upstream repo and create PRs from there.

Since this repo uses a private dependency, please run:

```shell
export GOPRIVATE=github.com/celestiaorg/rsema1dg
```

to avoid errors like:

```logs
$ make build
--> Updating go.mod
--> Updating go.mod in ./test/docker-e2e
go: downloading github.com/supranational/blst v0.3.14
github.com/celestiaorg/rsema1d@v0.0.0-20251031140154-a36e18b9e940: verifying module: github.com/celestiaorg/rsema1d@v0.0.0-20251031140154-a36e18b9e940: reading https://sum.golang.org/lookup/github.com/celestiaorg/rsema1d@v0.0.0-20251031140154-a36e18b9e940: 404 Not Found
	server response:
	not found: github.com/celestiaorg/rsema1d@v0.0.0-20251031140154-a36e18b9e940: invalid version: git ls-remote -q https://github.com/celestiaorg/rsema1d in /tmp/gopath/pkg/mod/cache/vcs/80359c499da36dd4a46616f56c0030c37ec779213b21ed63f7ab15033a4dd267: exit status 128:
		fatal: could not read Username for 'https://github.com': terminal prompts disabled
	Confirm the import path was entered correctly.
	If this is a private repository, see https://golang.org/doc/faq#git_https for additional information.
make: *** [mod] Error 1
```

## Scripts

Start a local single node devnet with Fibre DA enabled:

```bash
./scripts/single-node-fibre.sh
```

In a separate terminal, start submitting Fibre blobs:

```bash
./scripts/submit-fibre-blobs.sh
```

## Links

- <https://github.com/celestiaorg/fibre-da-spec>
- <https://github.com/celestiaorg/rsema1d>
