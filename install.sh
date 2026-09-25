set -e
VERSION="$(git describe --tags --always --dirty)"
go install -ldflags="-X dev/internal/version.Version=${VERSION#v}" ./cmd/...
dev daemon stop || true
cp ~/go/bin/dev ~/.local/bin/dev.new
sudo setcap CAP_NET_BIND_SERVICE=+eip ~/.local/bin/dev.new
mv ~/.local/bin/dev.new ~/.local/bin/dev
dev daemon start
