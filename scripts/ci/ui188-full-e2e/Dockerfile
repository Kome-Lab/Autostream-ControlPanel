ARG NODE_IMAGE=node:26.5.0-bookworm-slim@sha256:2d49d876e96237d76de412761cf05dbfe5aee325cc4406a4d41d5824c5bb8beb
ARG GO_IMAGE=golang:1.26.5-trixie@sha256:f1a132429b98724a904e9b3bdbaed399d8f923203c3e5170e6def66d0a7cc04c
FROM ${NODE_IMAGE} AS node
FROM ${GO_IMAGE}
ENV DEBIAN_FRONTEND=noninteractive GOWORK=off GOMAXPROCS=2 CMAKE_BUILD_PARALLEL_LEVEL=2 VCPKG_MAX_CONCURRENCY=2
RUN apt-get update && apt-get install -y --no-install-recommends python3 ffmpeg mariadb-client openssl chromium chromium-sandbox xvfb xauth fonts-noto-cjk iptables iproute2 util-linux ca-certificates cmake ninja-build g++ make pkg-config autoconf automake libtool zip unzip tar curl git libatomic1 && rm -rf /var/lib/apt/lists/*
COPY --from=node /usr/local/ /usr/local/
COPY cp /r6/cp
COPY bot /r6/bot
COPY encoder /r6/encoder
COPY worker /r6/worker
COPY cp/scripts/ci/ui188-full-e2e /opt/ui188
RUN cp /r6/cp/scripts/ci/ui188-r4-chain/packet_capture.py /opt/ui188/original_packet_capture.py
RUN bash /opt/ui188/build.sh
RUN useradd --uid 1000 --create-home ui188 && mkdir -p /var/lib/autostream/worker /r6/data /r6/diagnostic && chown -R 1000:1000 /r6 /home/ui188 /var/lib/autostream/worker
WORKDIR /r6
