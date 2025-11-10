FROM python:3.11-slim

ARG TARGETARCH
ARG GOLANG_VERSION=1.22.2

# System dependencies for FMIL, Go build, and pythonfmu
RUN apt-get update \
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

# Install Go toolchain
ENV PATH="/usr/local/go/bin:${PATH}"
RUN curl -fsSL "https://go.dev/dl/go${GOLANG_VERSION}.linux-amd64.tar.gz" | tar -C /usr/local -xz

# Build and install FMIL (fmilib)
RUN git clone https://github.com/modelon-community/fmi-library.git /tmp/fmi-library \
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
COPY scripts/certs/ /tmp/certs/
RUN set -eux; \
    FOUND_CERT=$(find /tmp/certs -maxdepth 1 -type f \( -name '*.crt' -o -name '*.pem' \) -print -quit || true); \
    if [ -n "$FOUND_CERT" ]; then \
        cp -a /tmp/certs/. /usr/local/share/ca-certificates/; \
        update-ca-certificates; \
    fi
RUN pip install --no-cache-dir -r /tmp/pythonfmu-requirements.txt

# Rebuild pythonfmu exporter for the active architecture so generated FMUs ship
# with matching binaries.
RUN set -eux; \
    PYFMI_EXPORT_DIR=/usr/local/lib/python3.11/site-packages/pythonfmu/pythonfmu-export; \
    cd "$PYFMI_EXPORT_DIR"; \
    chmod +x build_unix.sh; \
    ./build_unix.sh; \
    rm -rf build

WORKDIR /app
COPY . /app

# Refresh trusted certificates if provided in the repo
RUN set -eux; \
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
    CACHE_ROOT=/app/create_fmu/artifacts/cache; \
    TARGET_DIR=/usr/local/lib/python3.11/site-packages/pythonfmu/resources; \
    if [ -d "$CACHE_ROOT/linux/pythonfmu_resources" ]; then \
        rm -rf "$TARGET_DIR"; \
        mkdir -p "$TARGET_DIR"; \
        cp -a "$CACHE_ROOT/linux/pythonfmu_resources/." "$TARGET_DIR/"; \
        if [ "$TARGETARCH" = "arm64" ] && [ -d "$CACHE_ROOT/apple/pythonfmu_resources" ]; then \
            cp -a "$CACHE_ROOT/apple/pythonfmu_resources/." "$TARGET_DIR/"; \
        fi; \
    fi

# Build FMUs with pythonfmu
RUN mkdir -p fmu/models && \
    python -m pythonfmu build -f fmu/models/producer_fmu.py -d fmu/models && \
    python -m pythonfmu build -f fmu/models/consumer_fmu.py -d fmu/models && \
    echo 'Built FMUs to /app/fmu/models'

# Build Go workflow binaries (FMIL via cgo)
RUN mkdir -p /app/bin && \
    cd /app && \
    go build -o /app/bin/cads-workflow-runner ./orchestrator/service/cmd/cads-workflow-runner && \
    go build -o /app/bin/cads-workflow-service ./orchestrator/service/cmd/cads-workflow-service

# Default command
CMD ["/app/bin/cads-workflow-runner", "--workflow", "workflows/python_chain.yaml"]
