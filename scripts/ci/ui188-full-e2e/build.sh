#!/usr/bin/env bash
set -euo pipefail
umask 077
test "$(go env GOVERSION)" = go1.26.5
test "$(node --version)" = v26.5.0
mkdir /r6/bin
cd /r6/cp/web
npm ci --no-audit --no-fund
npm run build
cd /r6/cp
go build -mod=readonly -p 1 -o /r6/bin/cp ./cmd/control-panel
cd /r6/encoder
go build -mod=readonly -p 1 -o /r6/bin/encoder ./cmd/encoder-recorder
cd /r6/worker
go build -mod=readonly -p 1 -o /r6/bin/worker ./cmd/worker
cd /r6/bot
cpp_root=/r6/bot/third_party/discordgo/dave/libdave/cpp
vcpkg_root="$cpp_root/vcpkg"
"$vcpkg_root/bootstrap-vcpkg.sh" -disableMetrics
cmake -S "$cpp_root" -B "$cpp_root/build" -G Ninja \
  -DCMAKE_BUILD_TYPE=Release \
  -DVCPKG_MANIFEST_DIR="$cpp_root/vcpkg-alts/openssl_3" \
  -DCMAKE_TOOLCHAIN_FILE="$vcpkg_root/scripts/buildsystems/vcpkg.cmake" \
  -DBUILD_SHARED_LIBS=OFF -DENABLE_SANITIZERS=OFF -DPERSISTENT_KEYS=OFF \
  -DTESTING=OFF -DINSTALL_VCPKG_LICENSES=OFF \
  -DCMAKE_INSTALL_PREFIX="$cpp_root/build/install"
cmake --build "$cpp_root/build" --target libdave --config Release --parallel 2
vcpkg_libdir=$(find "$cpp_root/build/vcpkg_installed" -maxdepth 2 -type d -name lib -print -quit)
test -n "$vcpkg_libdir"
vcpkg_libs=$(find "$vcpkg_libdir" -maxdepth 1 -type f -name '*.a' -print | sort | tr '\n' ' ')
export CGO_ENABLED=1
export CGO_CFLAGS="-I$cpp_root/includes"
export CGO_LDFLAGS="-L$vcpkg_libdir $cpp_root/build/libdave.a -Wl,--start-group $vcpkg_libs -Wl,--end-group -lstdc++ -lm -ldl -lpthread"
go build -mod=readonly -p 1 -o /r6/bin/bot-product ./cmd/discord-bot
# Product source was already byte-verified. The separate tagged adapter is never a release BOT.
cp -a /r6/bot /r6/bot-adapter
cd /r6/bot-adapter
git -c core.autocrlf=false apply --check /opt/ui188/bot-adapter.patch
git -c core.autocrlf=false apply /opt/ui188/bot-adapter.patch
go test -mod=readonly -p 1 -tags ui188_chain -c -o /r6/bin/bot-test ./cmd/discord-bot
# No media-quality PASS is inferred from compilation.
python3 -B /opt/ui188/port.py
python3 -B /opt/ui188/seal.py build
