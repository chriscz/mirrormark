# mirrormark

mirrormark keeps chosen bookmark folders the same across browser profiles on
one computer. It is a browser extension plus one Go program, and it stores
everything in YAML files you can read. No server, no account, no network.

Status: in development. Google Chrome on Linux comes first.

## Development

Everything builds and tests in a Docker image; nothing is installed on your
machine except Docker.

    bin/dev run make          # format check, vet, unit tests, build
    bin/dev shell             # a shell in the dev container

The binary lands in `build/mirrormark`.

## Licence

MPL-2.0. See `LICENSE`.
