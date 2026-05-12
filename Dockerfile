ARG BUILDPLATFORM
ARG TARGETARCH
ARG GOLANG_VERSION=1.22.2

FROM --platform=$BUILDPLATFORM debian:bookworm-slim AS go-builder

ARG TARGETARCH
ARG GOLANG_VERSION

RUN echo "[go-builder] Installing build dependencies for ${TARGETARCH}" \
    && apt-get update \
    && build_arch="$(dpkg --print-architecture)" \
    && case "${TARGETARCH}" in \
        amd64) target_deb_arch=amd64; cross_pkgs="gcc-x86-64-linux-gnu g++-x86-64-linux-gnu"; target_cc=x86_64-linux-gnu-gcc ;; \
        arm64) target_deb_arch=arm64; cross_pkgs="gcc-aarch64-linux-gnu g++-aarch64-linux-gnu"; target_cc=aarch64-linux-gnu-gcc ;; \
        *) echo "Unsupported TARGETARCH: ${TARGETARCH}" >&2; exit 1 ;; \
    esac \
    && if [ "$target_deb_arch" != "$build_arch" ]; then dpkg --add-architecture "$target_deb_arch"; apt-get update; else cross_pkgs=""; fi \
    && DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends \
        ca-certificates \
        cmake \
        curl \
        git \
        file \
        make \
        pkg-config \
        ${cross_pkgs} \
        "libpugixml-dev:${target_deb_arch}" \
        "libxml2-dev:${target_deb_arch}" \
        "libzip-dev:${target_deb_arch}" \
        "zlib1g-dev:${target_deb_arch}" \
    && if [ "$target_deb_arch" = "$build_arch" ]; then DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends build-essential; fi \
    && rm -rf /var/lib/apt/lists/*

ARG CADS_CERTS_SHA=none
COPY scripts/certs/ /tmp/certs/
RUN set -eux; \
    echo "[go-builder] Certificate bundle digest: ${CADS_CERTS_SHA}"; \
    FOUND_CERT=$(find /tmp/certs -maxdepth 1 -type f \( -name '*.crt' -o -name '*.pem' \) -print -quit || true); \
    if [ -n "$FOUND_CERT" ]; then \
        cp -a /tmp/certs/. /usr/local/share/ca-certificates/; \
        update-ca-certificates; \
    fi

ENV SSL_CERT_FILE=/etc/ssl/certs/ca-certificates.crt
ENV GIT_SSL_CAINFO=/etc/ssl/certs/ca-certificates.crt
ENV PATH="/usr/local/go/bin:${PATH}"

RUN set -eux; \
    build_arch="$(uname -m)"; \
    case "$build_arch" in \
        x86_64) go_arch=amd64 ;; \
        aarch64) go_arch=arm64 ;; \
        *) echo "Unsupported build architecture: $build_arch" >&2; exit 1 ;; \
    esac; \
    echo "[go-builder] Installing Go ${GOLANG_VERSION} for linux-${go_arch}"; \
    curl -fsSL "https://go.dev/dl/go${GOLANG_VERSION}.linux-${go_arch}.tar.gz" | tar -C /usr/local -xz

RUN set -eux; \
    case "${TARGETARCH}" in \
        amd64) target_processor=x86_64; target_cc=x86_64-linux-gnu-gcc; target_cxx=x86_64-linux-gnu-g++ ;; \
        arm64) target_processor=aarch64; target_cc=aarch64-linux-gnu-gcc; target_cxx=aarch64-linux-gnu-g++ ;; \
    esac; \
    if ! command -v "$target_cc" >/dev/null 2>&1; then target_cc=gcc; target_cxx=g++; fi; \
    export CC="$target_cc" CXX="$target_cxx"; \
    echo "[go-builder] Cross-building FMIL for linux/${TARGETARCH}"; \
    git clone --depth 1 --branch master https://github.com/modelon-community/fmi-library.git /tmp/fmi-library; \
    cmake -S /tmp/fmi-library -B /tmp/fmi-library/build \
        -DCMAKE_BUILD_TYPE=Release \
        -DCMAKE_SYSTEM_NAME=Linux \
        -DCMAKE_SYSTEM_PROCESSOR="${target_processor}" \
        -DCMAKE_C_COMPILER="${target_cc}" \
        -DCMAKE_CXX_COMPILER="${target_cxx}" \
        -DFMILIB_BUILD_TESTS=OFF \
        -DFMILIB_GENERATE_DOXYGEN_DOC=OFF \
        -DFMILIB_BUILD_STATIC_LIB=OFF \
        -DFMILIB_BUILD_SHARED_LIB=ON \
        -DCMAKE_INSTALL_PREFIX=/opt/fmil-target; \
    cmake --build /tmp/fmi-library/build -j"$(nproc)"; \
    cmake --install /tmp/fmi-library/build; \
    rm -rf /tmp/fmi-library

WORKDIR /src/orchestrator/service
COPY orchestrator/service/go.mod orchestrator/service/go.sum ./
RUN go mod download
COPY orchestrator/service/ ./
RUN set -eux; \
    case "${TARGETARCH}" in \
        amd64) target_cc=x86_64-linux-gnu-gcc; target_cxx=x86_64-linux-gnu-g++ ;; \
        arm64) target_cc=aarch64-linux-gnu-gcc; target_cxx=aarch64-linux-gnu-g++ ;; \
    esac; \
    if ! command -v "$target_cc" >/dev/null 2>&1; then target_cc=gcc; target_cxx=g++; fi; \
    mkdir -p /out; \
    export GOWORK=off GOOS=linux GOARCH="${TARGETARCH}" CC="${target_cc}" CXX="${target_cxx}" CGO_ENABLED=1; \
    export CGO_CFLAGS="-I/opt/fmil-target/include"; \
    export CGO_CXXFLAGS="-I/opt/fmil-target/include"; \
    export CGO_LDFLAGS="-L/opt/fmil-target/lib"; \
    echo "[go-builder] Compiling Go workflow runner for linux/${TARGETARCH}"; \
    go build -trimpath -o /out/cads-workflow-runner ./cmd/cads-workflow-runner; \
    echo "[go-builder] Compiling dashboard service for linux/${TARGETARCH}"; \
    CGO_ENABLED=0 go build -trimpath -o /out/cads-workflow-service ./cmd/cads-workflow-service; \
    file /out/cads-workflow-runner /out/cads-workflow-service

FROM python:3.11-slim

ARG TARGETARCH
ARG CADS_CERTS_SHA=none

# System dependencies for FMIL runtime, pythonfmu, and FMU generation
RUN echo "[image] Installing base system dependencies" \
    && apt-get update \
    && DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends \
        build-essential \
        ca-certificates \
        cmake \
        libpugixml-dev \
        libxml2-dev \
        libzip-dev \
        pkg-config \
        unzip \
    && update-ca-certificates \
    && rm -rf /var/lib/apt/lists/*

# Ensure custom CA certificates are trusted before network downloads (e.g. Go tarball)
COPY scripts/certs/ /tmp/certs/
RUN set -eux; \
    echo "[image] Certificate bundle digest: ${CADS_CERTS_SHA}"; \
    echo "[image] Syncing bootstrap certificates from /tmp/certs"; \
    FOUND_CERT=$(find /tmp/certs -maxdepth 1 -type f \( -name '*.crt' -o -name '*.pem' \) -print -quit || true); \
    if [ -n "$FOUND_CERT" ]; then \
        cp -a /tmp/certs/. /usr/local/share/ca-certificates/; \
        update-ca-certificates; \
    fi

ENV SSL_CERT_FILE=/etc/ssl/certs/ca-certificates.crt
ENV REQUESTS_CA_BUNDLE=/etc/ssl/certs/ca-certificates.crt
ENV PIP_CERT=/etc/ssl/certs/ca-certificates.crt

COPY --from=go-builder /opt/fmil-target /opt/fmil

ENV FMIL_HOME=/opt/fmil
ENV LD_LIBRARY_PATH="${FMIL_HOME}/lib:${LD_LIBRARY_PATH}"
ENV PKG_CONFIG_PATH="${FMIL_HOME}/lib/pkgconfig:${PKG_CONFIG_PATH}"
ENV CGO_ENABLED=1
ENV CGO_CFLAGS="-I${FMIL_HOME}/include"
ENV CGO_CXXFLAGS="-I${FMIL_HOME}/include"
ENV CGO_LDFLAGS="-L${FMIL_HOME}/lib"
ENV GOWORK=off

# Python dependencies
COPY create_fmu/requirements.txt /tmp/pythonfmu-requirements.txt
COPY create_fmu/patch_pythonfmu_export.py /tmp/patch_pythonfmu_export.py
RUN echo "[image] Installing pythonfmu requirements inside the image" \
    && pip install --no-cache-dir -r /tmp/pythonfmu-requirements.txt
RUN echo "[image] Applying pythonfmu exporter patch" \
    && python /tmp/patch_pythonfmu_export.py

# Rebuild pythonfmu exporter for the active architecture so generated FMUs ship
# with matching binaries.
RUN set -eux; \
    echo "[image] Compiling pythonfmu exporter artifacts"; \
    PYFMI_EXPORT_DIR=/usr/local/lib/python3.11/site-packages/pythonfmu/pythonfmu-export; \
    cd "$PYFMI_EXPORT_DIR"; \
    chmod +x build_unix.sh; \
    ./build_unix.sh; \
    rm -rf build

WORKDIR /app
COPY . /app

# Refresh trusted certificates if provided in the repo
RUN set -eux; \
    echo "[image] Refreshing trusted certificates from repo"; \
    CERT_SRC=/app/scripts/certs; \
    FIRST_CERT=""; \
    if [ -d "$CERT_SRC" ]; then \
        FIRST_CERT=$(find "$CERT_SRC" -maxdepth 1 -type f \( -name '*.crt' -o -name '*.pem' \) -print -quit || true); \
    fi; \
    if [ -n "$FIRST_CERT" ]; then \
        cp -a "$CERT_SRC"/. /usr/local/share/ca-certificates/; \
        update-ca-certificates; \
    fi

# Optionally seed pythonfmu runtime resources from cached artifacts
RUN set -eux; \
    echo "[image] Checking for cached pythonfmu resource bundles"; \
    CACHE_ROOT=/app/create_fmu/artifacts/cache; \
    TARGET_DIR=/usr/local/lib/python3.11/site-packages/pythonfmu/resources; \
    PY_VERSION=$(python3 -c 'import platform; print(platform.python_version())'); \
    copied_any=false; \
    copy_profile() { \
        local src="$1" profile="$2" host_version; \
        if [ ! -d "$src" ]; then \
            return 1; \
        fi; \
        if [ ! -f "$src/.python-version" ]; then \
            echo "[pythonfmu] Skipping cache for ${profile}: missing .python-version metadata." >&2; \
            return 1; \
        fi; \
        host_version="$(cat "$src/.python-version")"; \
        if [ "$host_version" != "$PY_VERSION" ]; then \
            echo "[pythonfmu] Skipping cache for ${profile}: host Python ${host_version} != image Python ${PY_VERSION}." >&2; \
            return 1; \
        fi; \
        if [ "$copied_any" = false ]; then \
            rm -rf "$TARGET_DIR"; \
            mkdir -p "$TARGET_DIR"; \
        fi; \
        echo "[pythonfmu] Installing cached resources for ${profile} (Python ${host_version})."; \
        cp -a "$src/." "$TARGET_DIR/"; \
        copied_any=true; \
        return 0; \
    }; \
    if copy_profile "$CACHE_ROOT/linux/pythonfmu_resources" "linux"; then \
        if [ "$TARGETARCH" = "arm64" ]; then \
            copy_profile "$CACHE_ROOT/apple/pythonfmu_resources" "apple" || true; \
        fi; \
    fi; \
    if [ "$copied_any" = false ]; then \
        echo "[pythonfmu] No compatible cached resources; keeping exporter output built in this image." >&2; \
    fi

# Build FMUs with pythonfmu
RUN echo "[image] Building bundled demo FMUs" && \
    mkdir -p fmu/models && \
    python -m pythonfmu build -f create_fmu/producer_fmu.py -d fmu/models && \
    python -m pythonfmu build -f create_fmu/consumer_fmu.py -d fmu/models && \
    python -m pythonfmu build -f create_fmu/ae_event_stats_fmu.py -d fmu/models && \
    for replica in create_fmu/storhy_replicas/*_fmu.py; do \
        python -m pythonfmu build -f "$replica" -d fmu/models create_fmu/storhy_replicas/storhy_replica_common.py; \
    done && \
    echo 'Built FMUs to /app/fmu/models'

# Install Go workflow binaries built for the target architecture.
COPY --from=go-builder /out/cads-workflow-runner /app/bin/cads-workflow-runner
COPY --from=go-builder /out/cads-workflow-service /app/bin/cads-workflow-service
RUN set -eux; \
    chmod +x /app/bin/cads-workflow-runner /app/bin/cads-workflow-service

# Default command
CMD ["/app/bin/cads-workflow-runner", "--workflow", "workflows/tests/python_chain.yaml"]
