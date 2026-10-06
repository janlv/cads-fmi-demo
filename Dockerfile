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
# Version string baked into both binaries (e.g. `git describe --always --dirty`).
ARG CADS_VERSION=dev
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
    version_ldflags="-X github.com/norceresearch/cads-fmi-demo/orchestrator/service.Version=${CADS_VERSION}"; \
    echo "[go-builder] Compiling Go workflow runner for linux/${TARGETARCH} (version ${CADS_VERSION})"; \
    go build -trimpath -ldflags "${version_ldflags}" -o /out/cads-workflow-runner ./cmd/cads-workflow-runner; \
    echo "[go-builder] Compiling dashboard service for linux/${TARGETARCH} (version ${CADS_VERSION})"; \
    CGO_ENABLED=0 go build -trimpath -ldflags "${version_ldflags}" -o /out/cads-workflow-service ./cmd/cads-workflow-service; \
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
RUN echo "[image] Applying pythonfmu/pythonfmu3 exporter patches" \
    && python /tmp/patch_pythonfmu_export.py --package all

# Rebuild pythonfmu exporter for the active architecture so generated FMUs ship
# with matching binaries.
RUN set -eux; \
    echo "[image] Compiling pythonfmu exporter artifacts"; \
    PYFMI_EXPORT_DIR=/usr/local/lib/python3.11/site-packages/pythonfmu/pythonfmu-export; \
    cd "$PYFMI_EXPORT_DIR"; \
    chmod +x build_unix.sh; \
    ./build_unix.sh; \
    rm -rf build

# Same for the pythonfmu3 (FMI 3.0) exporter. The wheel ships a prebuilt
# x86_64-linux library that is not linked against libpython; drop it so the
# FMI 3 FMUs only carry the exporter built (and patched) in this image.
RUN set -eux; \
    echo "[image] Compiling pythonfmu3 (FMI 3.0) exporter artifacts"; \
    case "$(uname -m)" in \
        x86_64) fmi3_arch=x86_64 ;; \
        aarch64|arm64) fmi3_arch=aarch64 ;; \
        *) echo "Unsupported architecture for pythonfmu3: $(uname -m)" >&2; exit 1 ;; \
    esac; \
    PYFMU3_DIR=/usr/local/lib/python3.11/site-packages/pythonfmu3; \
    rm -rf "$PYFMU3_DIR/resources/binaries/x86_64-linux" "$PYFMU3_DIR/resources/binaries/aarch64-linux"; \
    cd "$PYFMU3_DIR/pythonfmu-export"; \
    sh build_unix.sh; \
    rm -rf build; \
    lib="$PYFMU3_DIR/resources/binaries/${fmi3_arch}-linux/libpythonfmu-export.so"; \
    test -f "$lib"; \
    ldd "$lib" | grep -q libpython3 || { echo "pythonfmu3 exporter is not linked against libpython: $lib" >&2; exit 1; }

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
    echo "[image] Building STOR-HY FMI 3.0 FMUs via pythonfmu3" && \
    for model in create_fmu/storhy_fmi3/*_fmi3.py; do \
        python -m pythonfmu3 build -f "$model" -d fmu/models \
            create_fmu/storhy_fmi3/storhy_fmi3_common.py \
            create_fmu/storhy_fmi3/storhy_fmi3_models.py || exit 1; \
    done && \
    case "$(uname -m)" in aarch64|arm64) fmi3_arch=aarch64 ;; *) fmi3_arch=x86_64 ;; esac && \
    for fmu in fmu/models/*Fmi3.fmu; do \
        unzip -l "$fmu" | grep -q "binaries/${fmi3_arch}-linux/" \
            || { echo "[image] $fmu lacks binaries/${fmi3_arch}-linux/" >&2; exit 1; }; \
    done && \
    echo 'Built FMUs to /app/fmu/models'

# Install Go workflow binaries built for the target architecture.
COPY --from=go-builder /out/cads-workflow-runner /app/bin/cads-workflow-runner
COPY --from=go-builder /out/cads-workflow-service /app/bin/cads-workflow-service
RUN set -eux; \
    chmod +x /app/bin/cads-workflow-runner /app/bin/cads-workflow-service

# Default command
CMD ["/app/bin/cads-workflow-runner", "--workflow", "workflows/tests/python_chain.yaml"]
