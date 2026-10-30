# ARG instructions do not create additional layers. Instead, next layers will
# concatenate them. Also, we have to repeat ARG instructions in each build
# stage that uses them.
ARG GOLANG_VERSION=1.27.3

# ----------------------------------------------
# Bussum binary build stage
# ----------------------------------------------
FROM golang:$GOLANG_VERSION AS bussum-binary-stage

ARG GOTENBERG_VERSION=snapshot
ENV CGO_ENABLED=0

# Define the working directory outside of $GOPATH (we're using go modules).
WORKDIR /home

# Install module dependencies.
COPY go.mod go.sum ./

RUN go mod download \
    && go mod verify

# Copy the source code.
COPY cmd ./cmd
COPY pkg ./pkg

RUN go build -o bussum -ldflags "-s -w -X 'github.com/gotenberg/gotenberg/v8/cmd.Version=$GOTENBERG_VERSION'" cmd/gotenberg/main.go \
    && go build -o bussum-chromium -ldflags "-s -w -X 'github.com/gotenberg/gotenberg/v8/cmd.Version=$GOTENBERG_VERSION'" cmd/gotenberg-chromium/main.go \
    && go build -o bussum-libreoffice -ldflags "-s -w -X 'github.com/gotenberg/gotenberg/v8/cmd.Version=$GOTENBERG_VERSION'" cmd/gotenberg-libreoffice/main.go

# ----------------------------------------------
# Downloader stage
# ----------------------------------------------
FROM debian:13-slim AS downloader-stage

RUN apt-get update -qq \
    && DEBIAN_FRONTEND=noninteractive apt-get install -y -qq --no-install-recommends curl ca-certificates

WORKDIR /downloads

RUN curl -Ls https://raw.githubusercontent.com/gotenberg/unoconverter/v0.5.0/unoconv -o unoconverter \
    && chmod +x unoconverter

# ----------------------------------------------
# Base image stage
# ----------------------------------------------
FROM debian:13-slim AS base-image-stage

ARG TIMEZONE=UTC
ENV TZ=$TIMEZONE

# ----------------------------------------------
# Common stage
# ----------------------------------------------
FROM base-image-stage AS common-stage

ARG GOTENBERG_USER_GID=1001
ARG GOTENBERG_USER_UID=1001

# See the note on APT_SNAPSHOT in custom-jre-stage. Declaring it here covers
# every "apt-get upgrade" in the gotenberg, gotenberg-chromium and
# gotenberg-libreoffice targets too, since all three branch from this stage.
ARG APT_SNAPSHOT=""

# Create a non-root user.
# All processes in the Docker container will run with this dedicated user.
RUN echo "apt snapshot: $APT_SNAPSHOT" \
    && groupadd --gid "$GOTENBERG_USER_GID" gotenberg \
    && useradd --uid "$GOTENBERG_USER_UID" --gid gotenberg --shell /bin/bash --home /home/gotenberg --no-create-home gotenberg \
    && mkdir /home/gotenberg \
    && chown gotenberg: /home/gotenberg

# Note: tini is a helper for reaping zombie processes.
RUN apt-get update -qq \
    && apt-get upgrade -yqq \
    && DEBIAN_FRONTEND=noninteractive apt-get install -y -qq --no-install-recommends \
        ca-certificates \
        tini \
        # Many users rely on curl for Docker health checks.
        curl \
        # Install fonts.
        # Essential metric-compatible fonts for LibreOffice layout fidelity (replaces MS Fonts).
        fonts-crosextra-carlito \
        fonts-crosextra-caladea \
        fonts-liberation \
        fonts-liberation2 \
        # Reliable general-purpose fallback for Chromium.
        fonts-dejavu \
        # Unified CJK (Chinese, Japanese, Korean) support.
        fonts-noto-cjk \
        # Standard Emoji support.
        fonts-noto-color-emoji \
        # Tofu prevention.
        fonts-noto-core \
        # Poppler pdftohtml for PDF to Markdown. --no-install-recommends
        # leaves out poppler-data (CJK CMap files).
        poppler-utils \
    # See https://github.com/nextcloud/docker/issues/380.
    && mkdir -p /usr/share/man/man1 \
    # Cleanup.
    && rm -rf /var/lib/apt/lists/* /tmp/* /var/tmp/*

# Support for arbitrary user IDs (OpenShift).
# See:
# https://github.com/gotenberg/gotenberg/issues/1049.
# https://docs.redhat.com/en/documentation/openshift_container_platform/4.15/html/images/creating-images#use-uid_create-images.
RUN usermod -aG root gotenberg \
    && chgrp -R 0 /home/gotenberg \
    && chmod -R g=u /home/gotenberg

# COPY instructions last to maximize cache reuse when only source code changes.

# Improve fonts subpixel hinting and smoothing.
# Credits:
# https://github.com/arachnys/athenapdf/issues/69.
# https://github.com/arachnys/athenapdf/commit/ba25a8d80a25d08d58865519c4cd8756dc9a336d.
COPY --link build/fonts.conf /etc/fonts/conf.d/100-gotenberg.conf

# Copy the unoconverter script used by LibreOffice.
COPY --link --from=downloader-stage /downloads/unoconverter /usr/bin/unoconverter

# Set default characterset encoding to UTF-8.
# See:
# https://github.com/gotenberg/gotenberg/issues/104
# https://github.com/gotenberg/gotenberg/issues/730
ENV LANG=C.UTF-8
ENV LC_ALL=C.UTF-8

# Poppler pdftohtml converts PDF input to Markdown.
ENV PDFTOHTML_BIN_PATH=/usr/bin/pdftohtml

# Capture backing-binary versions at build time so the running process reports
# them on traces without spawning the binaries at startup or per request.
# See pkg/gotenberg/buildversions.go. Chromium and LibreOffice are captured in
# the variant stages below, where they are installed.
ENV GOTENBERG_VERSIONS_DIR_PATH=/opt/gotenberg/versions

COPY --link build/capture-version.sh /opt/gotenberg/capture-version.sh

RUN bash /opt/gotenberg/capture-version.sh "$GOTENBERG_VERSIONS_DIR_PATH" pdf "$PDFTOHTML_BIN_PATH" -v

# OpenTelemetry defaults (noop - no telemetry overhead unless explicitly enabled).
ENV OTEL_TRACES_EXPORTER=none
ENV OTEL_METRICS_EXPORTER=none
ENV OTEL_LOGS_EXPORTER=none

# ----------------------------------------------
# Final stage: full (Chromium + LibreOffice)
# ----------------------------------------------
FROM common-stage AS bussum

ARG GOTENBERG_VERSION=snapshot
ARG GOTENBERG_USER_GID=1001
ARG GOTENBERG_USER_UID=1001
ARG TMP_CHROMIUM_VERSION_PPC64EL="146.0.7680.80-1~deb13u1"

LABEL org.opencontainers.image.title="Bussum" \
    org.opencontainers.image.description="A Docker-based API for converting documents to Markdown. A fork of gotenberg/gotenberg." \
    org.opencontainers.image.version="$GOTENBERG_VERSION" \
    org.opencontainers.image.authors="Julien Neuhart <neuhart.julien@gmail.com>" \
    org.opencontainers.image.documentation="https://github.com/samma89/bussum" \
    org.opencontainers.image.source="https://github.com/samma89/bussum"

# Install Chromium.
# On ppc64el, the latest Chromium is broken, so we pin a known working
# version from snapshot.debian.org via debsnap.
RUN /bin/bash -c \
    'set -e &&\
    if [[ "$(dpkg --print-architecture)" == "ppc64el" ]]; then \
      apt-get update -qq &&\
      DEBIAN_FRONTEND=noninteractive apt-get install -y -qq --no-install-recommends devscripts &&\
      debsnap chromium-common "$TMP_CHROMIUM_VERSION_PPC64EL" -v --force --binary --architecture ppc64el &&\
      debsnap chromium "$TMP_CHROMIUM_VERSION_PPC64EL" -v --force --binary --architecture ppc64el &&\
      DEBIAN_FRONTEND=noninteractive apt-get install --fix-broken -y -qq --no-install-recommends "./binary-chromium-common/chromium-common_${TMP_CHROMIUM_VERSION_PPC64EL}_ppc64el.deb" "./binary-chromium/chromium_${TMP_CHROMIUM_VERSION_PPC64EL}_ppc64el.deb" &&\
      DEBIAN_FRONTEND=noninteractive apt-get purge -y -qq devscripts &&\
      rm -rf ./binary-chromium-common/* ./binary-chromium/*; \
    else \
      apt-get update -qq &&\
      apt-get upgrade -yqq &&\
      DEBIAN_FRONTEND=noninteractive apt-get install -y -qq --no-install-recommends chromium; \
    fi' \
    && rm -rf /var/lib/apt/lists/* /tmp/* /var/tmp/*

# Install LibreOffice & unoconverter.
RUN echo "deb http://deb.debian.org/debian trixie-backports main" >> /etc/apt/sources.list \
    && apt-get update -qq \
    && apt-get upgrade -yqq \
    && DEBIAN_FRONTEND=noninteractive apt-get install -y -qq --no-install-recommends \
        python3 python3-distutils-extra \
        # Install Hyphenation for LibreOffice.
        # Credits: https://wiki.archlinux.org/title/LibreOffice.
        hyphen-af hyphen-as hyphen-be hyphen-bg hyphen-bn hyphen-ca hyphen-cs hyphen-da hyphen-de hyphen-el \
        hyphen-en-gb hyphen-en-us hyphen-eo hyphen-es hyphen-fr hyphen-gl hyphen-gu hyphen-hi hyphen-hr hyphen-hu \
        hyphen-id hyphen-is hyphen-it hyphen-kn hyphen-lt hyphen-lv hyphen-ml hyphen-mn hyphen-mr hyphen-nl \
        hyphen-no hyphen-or hyphen-pa hyphen-pl hyphen-pt-br hyphen-pt-pt hyphen-ro hyphen-ru hyphen-sk hyphen-sl \
        hyphen-sr hyphen-sv hyphen-ta hyphen-te hyphen-th hyphen-uk hyphen-zu \
    && DEBIAN_FRONTEND=noninteractive apt-get install -y -qq --no-install-recommends -t trixie-backports \
        libreoffice-writer libreoffice-calc libreoffice-impress libreoffice-draw libreoffice-math python3-uno \
    # unoconverter will look for the Python binary, which has to be at version 3.
    && ln -s /usr/bin/python3 /usr/bin/python \
    # Cleanup.
    && rm -rf /var/lib/apt/lists/* /tmp/* /var/tmp/*

# COPY instructions last to maximize cache reuse.

# Copy the Gotenberg binary (full variant with all modules).
COPY --link --from=bussum-binary-stage /home/bussum /usr/bin/

# Copy unoconverter.
COPY --link --from=downloader-stage /downloads/unoconverter /usr/bin/unoconverter

# Copy dictionnaries so that hyphens work on Chromium.
# See https://github.com/gotenberg/gotenberg/issues/1293.
COPY --link --chown="$GOTENBERG_USER_UID:$GOTENBERG_USER_GID" build/chromium-hyphen-data /opt/gotenberg/chromium-hyphen-data

ENV CHROMIUM_BIN_PATH=/usr/bin/chromium
ENV CHROMIUM_HYPHEN_DATA_DIR_PATH=/opt/gotenberg/chromium-hyphen-data
ENV LIBREOFFICE_BIN_PATH=/usr/lib/libreoffice/program/soffice.bin
ENV UNOCONVERTER_BIN_PATH=/usr/bin/unoconverter

# Capture Chromium and LibreOffice versions now that both are installed.
RUN bash /opt/gotenberg/capture-version.sh "$GOTENBERG_VERSIONS_DIR_PATH" chromium "$CHROMIUM_BIN_PATH" --version \
    && bash /opt/gotenberg/capture-version.sh "$GOTENBERG_VERSIONS_DIR_PATH" libreoffice-api "$LIBREOFFICE_BIN_PATH" --version

USER gotenberg
WORKDIR /home/gotenberg

# Default API port.
EXPOSE 3000

ENTRYPOINT [ "/usr/bin/tini", "--" ]
CMD [ "bussum" ]

# ----------------------------------------------
# Final stage: Chromium only
# ----------------------------------------------
FROM common-stage AS bussum-chromium

ARG GOTENBERG_VERSION=snapshot
ARG GOTENBERG_USER_GID=1001
ARG GOTENBERG_USER_UID=1001
ARG TMP_CHROMIUM_VERSION_PPC64EL="146.0.7680.80-1~deb13u1"

LABEL org.opencontainers.image.title="Bussum (Chromium)" \
    org.opencontainers.image.description="Bussum image with Chromium, for HTML files and URLs. A fork of gotenberg/gotenberg." \
    org.opencontainers.image.version="$GOTENBERG_VERSION" \
    org.opencontainers.image.authors="Julien Neuhart <neuhart.julien@gmail.com>" \
    org.opencontainers.image.documentation="https://github.com/samma89/bussum" \
    org.opencontainers.image.source="https://github.com/samma89/bussum"

# Install Chromium.
# On ppc64el, the latest Chromium is broken, so we pin a known working
# version from snapshot.debian.org via debsnap.
RUN /bin/bash -c \
    'set -e &&\
    if [[ "$(dpkg --print-architecture)" == "ppc64el" ]]; then \
      apt-get update -qq &&\
      DEBIAN_FRONTEND=noninteractive apt-get install -y -qq --no-install-recommends devscripts &&\
      debsnap chromium-common "$TMP_CHROMIUM_VERSION_PPC64EL" -v --force --binary --architecture ppc64el &&\
      debsnap chromium "$TMP_CHROMIUM_VERSION_PPC64EL" -v --force --binary --architecture ppc64el &&\
      DEBIAN_FRONTEND=noninteractive apt-get install --fix-broken -y -qq --no-install-recommends "./binary-chromium-common/chromium-common_${TMP_CHROMIUM_VERSION_PPC64EL}_ppc64el.deb" "./binary-chromium/chromium_${TMP_CHROMIUM_VERSION_PPC64EL}_ppc64el.deb" &&\
      DEBIAN_FRONTEND=noninteractive apt-get purge -y -qq devscripts &&\
      rm -rf ./binary-chromium-common/* ./binary-chromium/*; \
    else \
      apt-get update -qq &&\
      apt-get upgrade -yqq &&\
      DEBIAN_FRONTEND=noninteractive apt-get install -y -qq --no-install-recommends chromium; \
    fi' \
    && rm -rf /var/lib/apt/lists/* /tmp/* /var/tmp/*

# COPY instructions last to maximize cache reuse.

# Copy the Gotenberg binary (Chromium variant, no LibreOffice modules).
COPY --link --from=bussum-binary-stage /home/bussum-chromium /usr/bin/bussum

# Copy dictionnaries so that hyphens work on Chromium.
# See https://github.com/gotenberg/gotenberg/issues/1293.
COPY --link --chown="$GOTENBERG_USER_UID:$GOTENBERG_USER_GID" build/chromium-hyphen-data /opt/gotenberg/chromium-hyphen-data

ENV CHROMIUM_BIN_PATH=/usr/bin/chromium
ENV CHROMIUM_HYPHEN_DATA_DIR_PATH=/opt/gotenberg/chromium-hyphen-data
# No LibreOffice in this variant; override the default to use all available engines.
ENV PDFENGINES_CONVERT_ENGINES=

# Capture the Chromium version now that it is installed.
RUN bash /opt/gotenberg/capture-version.sh "$GOTENBERG_VERSIONS_DIR_PATH" chromium "$CHROMIUM_BIN_PATH" --version

USER gotenberg
WORKDIR /home/gotenberg

# Default API port.
EXPOSE 3000

ENTRYPOINT [ "/usr/bin/tini", "--" ]
CMD [ "bussum" ]

# ----------------------------------------------
# Final stage: LibreOffice only
# ----------------------------------------------
FROM common-stage AS bussum-libreoffice

ARG GOTENBERG_VERSION=snapshot

LABEL org.opencontainers.image.title="Bussum (LibreOffice)" \
    org.opencontainers.image.description="Bussum image with LibreOffice, for Office documents. A fork of gotenberg/gotenberg." \
    org.opencontainers.image.version="$GOTENBERG_VERSION" \
    org.opencontainers.image.authors="Julien Neuhart <neuhart.julien@gmail.com>" \
    org.opencontainers.image.documentation="https://github.com/samma89/bussum" \
    org.opencontainers.image.source="https://github.com/samma89/bussum"

# Install LibreOffice & unoconverter.
RUN echo "deb http://deb.debian.org/debian trixie-backports main" >> /etc/apt/sources.list \
    && apt-get update -qq \
    && apt-get upgrade -yqq \
    && DEBIAN_FRONTEND=noninteractive apt-get install -y -qq --no-install-recommends \
        python3 python3-distutils-extra \
        # Install Hyphenation for LibreOffice.
        # Credits: https://wiki.archlinux.org/title/LibreOffice.
        hyphen-af hyphen-as hyphen-be hyphen-bg hyphen-bn hyphen-ca hyphen-cs hyphen-da hyphen-de hyphen-el \
        hyphen-en-gb hyphen-en-us hyphen-eo hyphen-es hyphen-fr hyphen-gl hyphen-gu hyphen-hi hyphen-hr hyphen-hu \
        hyphen-id hyphen-is hyphen-it hyphen-kn hyphen-lt hyphen-lv hyphen-ml hyphen-mn hyphen-mr hyphen-nl \
        hyphen-no hyphen-or hyphen-pa hyphen-pl hyphen-pt-br hyphen-pt-pt hyphen-ro hyphen-ru hyphen-sk hyphen-sl \
        hyphen-sr hyphen-sv hyphen-ta hyphen-te hyphen-th hyphen-uk hyphen-zu \
    && DEBIAN_FRONTEND=noninteractive apt-get install -y -qq --no-install-recommends -t trixie-backports \
        libreoffice-writer libreoffice-calc libreoffice-impress libreoffice-draw libreoffice-math python3-uno \
    # unoconverter will look for the Python binary, which has to be at version 3.
    && ln -s /usr/bin/python3 /usr/bin/python \
    # Cleanup.
    && rm -rf /var/lib/apt/lists/* /tmp/* /var/tmp/*

# COPY instructions last to maximize cache reuse.

# Copy the Gotenberg binary (LibreOffice variant, no Chromium modules).
COPY --link --from=bussum-binary-stage /home/bussum-libreoffice /usr/bin/bussum

# Copy unoconverter.
COPY --link --from=downloader-stage /downloads/unoconverter /usr/bin/unoconverter

ENV LIBREOFFICE_BIN_PATH=/usr/lib/libreoffice/program/soffice.bin
ENV UNOCONVERTER_BIN_PATH=/usr/bin/unoconverter

# Capture the LibreOffice version now that it is installed.
RUN bash /opt/gotenberg/capture-version.sh "$GOTENBERG_VERSIONS_DIR_PATH" libreoffice-api "$LIBREOFFICE_BIN_PATH" --version

USER gotenberg
WORKDIR /home/gotenberg

# Default API port.
EXPOSE 3000

ENTRYPOINT [ "/usr/bin/tini", "--" ]
CMD [ "bussum" ]

# ==============================================================
# Cloud Run variants
# ==============================================================

# ----------------------------------------------
# Cloud Run: full (Chromium + LibreOffice)
# ----------------------------------------------
FROM bussum AS bussum-cloudrun

USER root

# For security reasons, the non-root user gotenberg does not own the Tini binary by default.
# However, some providers like Cloud Run from Google Cloud cannot start a Docker container in that case.
# See https://github.com/gotenberg/gotenberg/issues/90#issuecomment-543551353.
RUN chown gotenberg: /usr/bin/tini

ENV API_PORT_FROM_ENV=PORT
ENV CHROMIUM_AUTO_START=true
ENV LIBREOFFICE_AUTO_START=true
ENV WEBHOOK_ENABLE_SYNC_MODE=true
ENV GOTENBERG_BUILD_DEBUG_DATA=false
ENV LOG_STD_ENABLE_GCP_FIELDS=true

USER gotenberg

# ----------------------------------------------
# Cloud Run: Chromium only
# ----------------------------------------------
FROM bussum-chromium AS bussum-cloudrun-chromium

USER root

RUN chown gotenberg: /usr/bin/tini

ENV API_PORT_FROM_ENV=PORT
ENV CHROMIUM_AUTO_START=true
ENV WEBHOOK_ENABLE_SYNC_MODE=true
ENV GOTENBERG_BUILD_DEBUG_DATA=false
ENV LOG_STD_ENABLE_GCP_FIELDS=true

USER gotenberg

# ----------------------------------------------
# Cloud Run: LibreOffice only
# ----------------------------------------------
FROM bussum-libreoffice AS bussum-cloudrun-libreoffice

USER root

RUN chown gotenberg: /usr/bin/tini

ENV API_PORT_FROM_ENV=PORT
ENV LIBREOFFICE_AUTO_START=true
ENV WEBHOOK_ENABLE_SYNC_MODE=true
ENV GOTENBERG_BUILD_DEBUG_DATA=false
ENV LOG_STD_ENABLE_GCP_FIELDS=true

USER gotenberg

# ==============================================================
# AWS Lambda variants
# ==============================================================

# ----------------------------------------------
# AWS Lambda: full (Chromium + LibreOffice)
# ----------------------------------------------
FROM bussum AS bussum-aws-lambda

USER root

COPY --from=public.ecr.aws/awsguru/aws-lambda-adapter:1.1.0 /lambda-adapter /opt/extensions/lambda-adapter

ENV AWS_LWA_PORT=3000
ENV AWS_LWA_READINESS_CHECK_PATH=/health
ENV AWS_LWA_INVOKE_MODE=buffered
ENV API_PORT_FROM_ENV=AWS_LWA_PORT
ENV WEBHOOK_ENABLE_SYNC_MODE=true
ENV GOTENBERG_BUILD_DEBUG_DATA=false

USER gotenberg

# ----------------------------------------------
# AWS Lambda: Chromium only
# ----------------------------------------------
FROM bussum-chromium AS bussum-aws-lambda-chromium

USER root

COPY --from=public.ecr.aws/awsguru/aws-lambda-adapter:1.1.0 /lambda-adapter /opt/extensions/lambda-adapter

ENV AWS_LWA_PORT=3000
ENV AWS_LWA_READINESS_CHECK_PATH=/health
ENV AWS_LWA_INVOKE_MODE=buffered
ENV API_PORT_FROM_ENV=AWS_LWA_PORT
ENV WEBHOOK_ENABLE_SYNC_MODE=true
ENV GOTENBERG_BUILD_DEBUG_DATA=false

USER gotenberg

# ----------------------------------------------
# AWS Lambda: LibreOffice only
# ----------------------------------------------
FROM bussum-libreoffice AS bussum-aws-lambda-libreoffice

USER root

COPY --from=public.ecr.aws/awsguru/aws-lambda-adapter:1.1.0 /lambda-adapter /opt/extensions/lambda-adapter

ENV AWS_LWA_PORT=3000
ENV AWS_LWA_READINESS_CHECK_PATH=/health
ENV AWS_LWA_INVOKE_MODE=buffered
ENV API_PORT_FROM_ENV=AWS_LWA_PORT
ENV WEBHOOK_ENABLE_SYNC_MODE=true
ENV GOTENBERG_BUILD_DEBUG_DATA=false

USER gotenberg
