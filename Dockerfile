FROM python:3.11-slim

ARG TARGETARCH
ARG GOLANG_VERSION=1.22.2

# System dependencies for FMIL, Go build, and pythonfmu
RUN echo "[image] Installing base system dependencies" \
    && apt-get update \
    && DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends \
        build-essential \
        ca-certificates \
        cmake \
        curl \
        git \
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
    echo "[image] Syncing bootstrap certificates from /tmp/certs"; \
    FOUND_CERT=$(find /tmp/certs -maxdepth 1 -type f \( -name '*.crt' -o -name '*.pem' \) -print -quit || true); \
    if [ -n "$FOUND_CERT" ]; then \
        cp -a /tmp/certs/. /usr/local/share/ca-certificates/; \
        update-ca-certificates; \
    fi

ENV SSL_CERT_FILE=/etc/ssl/certs/ca-certificates.crt
ENV REQUESTS_CA_BUNDLE=/etc/ssl/certs/ca-certificates.crt
ENV PIP_CERT=/etc/ssl/certs/ca-certificates.crt

# Install Go toolchain
ENV PATH="/usr/local/go/bin:${PATH}"
RUN echo "[image] Installing Go ${GOLANG_VERSION}" \
    && curl -fsSL "https://go.dev/dl/go${GOLANG_VERSION}.linux-amd64.tar.gz" | tar -C /usr/local -xz

# Build and install FMIL (fmilib)
RUN echo "[image] Cloning and building FMIL" \
    && git clone https://github.com/modelon-community/fmi-library.git /tmp/fmi-library \
    && cmake -S /tmp/fmi-library -B /tmp/fmi-library/build \
        -DCMAKE_BUILD_TYPE=Release \
        -DCMAKE_INSTALL_PREFIX=/opt/fmil \
    && cmake --build /tmp/fmi-library/build -j"$(nproc)" \
    && cmake --install /tmp/fmi-library/build \
    && rm -rf /tmp/fmi-library

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
    python -m pythonfmu build -f fmu/models/producer_fmu.py -d fmu/models && \
    python -m pythonfmu build -f fmu/models/consumer_fmu.py -d fmu/models && \
    echo 'Built FMUs to /app/fmu/models'

# Build Go workflow binaries (FMIL via cgo). The Go module lives under orchestrator/service.
RUN set -eux; \
    echo "[image] Compiling Go workflow binaries"; \
    mkdir -p /app/bin; \
    cd /app/orchestrator/service; \
    go build -o /app/bin/cads-workflow-runner ./cmd/cads-workflow-runner; \
    go build -o /app/bin/cads-workflow-service ./cmd/cads-workflow-service

# Default command
CMD ["/app/bin/cads-workflow-runner", "--workflow", "workflows/python_chain.yaml"]
