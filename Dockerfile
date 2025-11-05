FROM python:3.11-slim

ARG TARGETARCH

# System deps
RUN apt-get update \
    && DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends \
        unzip \
        build-essential \
        cmake \
    && rm -rf /var/lib/apt/lists/*

# CA certificates (optional)
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates \
    && update-ca-certificates

ENV SSL_CERT_FILE=/etc/ssl/certs/ca-certificates.crt
ENV REQUESTS_CA_BUNDLE=/etc/ssl/certs/ca-certificates.crt
ENV PIP_CERT=/etc/ssl/certs/ca-certificates.crt

# Python deps
COPY requirements.txt /tmp/requirements.txt
COPY certs/ /tmp/certs/
RUN set -eux; \
    FOUND_CERT=$(find /tmp/certs -maxdepth 1 -type f \( -name '*.crt' -o -name '*.pem' \) -print -quit || true); \
    if [ -n "$FOUND_CERT" ]; then \
        cp -a /tmp/certs/. /usr/local/share/ca-certificates/; \
        update-ca-certificates; \
    fi
RUN pip install --no-cache-dir -r /tmp/requirements.txt

# FMPy ships logger binaries only for x86_64; provide a no-op stub on arm64
RUN if [ "$TARGETARCH" = "arm64" ]; then \
        printf '%s\n' \
'#include <stddef.h>' \
'#ifdef __cplusplus' \
'extern "C" {' \
'#endif' \
'void addLoggerProxy(void* callbacks) {' \
'    (void)callbacks;' \
'}' \
'#ifdef __cplusplus' \
'}' \
'#endif' \
        > /tmp/fmpy_logger_stub.c; \
        gcc -shared -fPIC -o /usr/local/lib/python3.11/site-packages/fmpy/logging/linux64/logging.so /tmp/fmpy_logger_stub.c; \
        rm /tmp/fmpy_logger_stub.c; \
    fi

# Rebuild pythonfmu exporter for current architecture so the generated FMUs ship
# with native binaries (arm64 on Apple Silicon, etc.).
RUN set -eux; \
    PYFMI_EXPORT_DIR=/usr/local/lib/python3.11/site-packages/pythonfmu/pythonfmu-export; \
    cd "$PYFMI_EXPORT_DIR"; \
    chmod +x build_unix.sh; \
    ./build_unix.sh; \
    rm -rf build

# Workdir
WORKDIR /app

# Copy project sources
COPY . /app

# Refresh trusted certificates if provided in repo
RUN set -eux; \
    CERT_SRC=/app/certs; \
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
    CACHE_ROOT=/app/fmu/artifacts/cache; \
    TARGET_DIR=/usr/local/lib/python3.11/site-packages/pythonfmu/resources; \
    if [ -d "$CACHE_ROOT/linux/pythonfmu_resources" ]; then \
        rm -rf "$TARGET_DIR"; \
        mkdir -p "$TARGET_DIR"; \
        cp -a "$CACHE_ROOT/linux/pythonfmu_resources/." "$TARGET_DIR/"; \
        if [ "$TARGETARCH" = "arm64" ] && [ -d "$CACHE_ROOT/apple/pythonfmu_resources" ]; then \
            cp -a "$CACHE_ROOT/apple/pythonfmu_resources/." "$TARGET_DIR/"; \
        fi; \
    fi

# Build FMUs (pythonfmu CLI)
RUN mkdir -p fmu/artifacts/build && \
    python -m pythonfmu build -f fmu/models/producer_fmu.py -d fmu/artifacts/build && \
    python -m pythonfmu build -f fmu/models/consumer_fmu.py -d fmu/artifacts/build && \
    echo 'Built FMUs to /app/fmu/artifacts/build'

# Default command: run orchestrator
CMD ["python", "orchestrator/run.py"]
