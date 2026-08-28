# Taskman Odoo test container image.
#
# docs/DECISIONS.md: ghcr.io/solvti/odoo-env:<version> is unreachable in this sandbox
# (no registry credentials). This Dockerfile builds a from-source equivalent on top of
# python:3.12-slim, taking the real base image as a build-arg so swapping back once
# credentials exist is `--build-arg BASE_IMAGE=ghcr.io/solvti/odoo-env:19.0` and nothing
# else changes in the compose template.
#
# The entrypoint deliberately just sleeps (technical-spec.md §6.1): the container's
# lifetime is independent of the odoo-bin process, which is started/killed/restarted
# per deploy cycle via `docker exec`.
ARG BASE_IMAGE=python:3.12-slim
FROM ${BASE_IMAGE}

ENV DEBIAN_FRONTEND=noninteractive \
    LANG=en_US.UTF-8 \
    PYTHONUNBUFFERED=1

RUN apt-get update && apt-get install -y --no-install-recommends \
        build-essential \
        locales \
        git \
        curl \
        ca-certificates \
        postgresql-client \
        libpq-dev \
        libxml2-dev \
        libxslt1-dev \
        libjpeg-dev \
        zlib1g-dev \
        libfreetype6-dev \
        liblcms2-dev \
        libwebp-dev \
        libharfbuzz-dev \
        libfribidi-dev \
        libxcb1-dev \
        libssl-dev \
        libffi-dev \
        libsasl2-dev \
        libldap2-dev \
        node-less \
        procps \
    && sed -i '/en_US.UTF-8/s/^# //' /etc/locale.gen \
    && locale-gen \
    && rm -rf /var/lib/apt/lists/*

# Built with build context = the version root (e.g. fixtures/repos/17.0/ or
# odoo-env-19/), so this resolves to that version's own odoo/requirements.txt.
# Baked in at build time (not compose-up time) so repeated deploy cycles against a
# warm container never pay a pip-install cost — matches spec §6.2's "keep it fast"
# goal for the deploy loop. One image per Odoo version, tagged taskman-odoo-test:<version>.
COPY odoo/requirements.txt /tmp/odoo-requirements.txt
RUN pip install --no-cache-dir --upgrade pip \
    && pip install --no-cache-dir -r /tmp/odoo-requirements.txt

WORKDIR /
ENTRYPOINT ["sleep", "infinity"]
