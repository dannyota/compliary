#!/usr/bin/env python3
"""Render server and one-off migration task definitions from the live revision."""

from __future__ import annotations

import argparse
import copy
import json
from pathlib import Path
from typing import Any

RESPONSE_ONLY_FIELDS = (
    "taskDefinitionArn",
    "revision",
    "status",
    "requiresAttributes",
    "compatibilities",
    "registeredAt",
    "registeredBy",
)

REQUIRED_DATABASE_ENV = {
    "COMPLIARY_DATABASE_HOST",
    "COMPLIARY_DATABASE_PORT",
    "COMPLIARY_DATABASE_USER",
    "COMPLIARY_DATABASE_NAME",
    "COMPLIARY_DATABASE_SSLMODE",
}


def render(
    base: dict[str, Any],
    image: str,
    migrate_family: str,
) -> tuple[dict[str, Any], dict[str, Any]]:
    """Return registerable server and migration definitions."""
    if "@sha256:" not in image:
        raise ValueError("image must be pinned by sha256 digest")
    if not migrate_family:
        raise ValueError("migration task family is required")

    template = copy.deepcopy(base)
    for key in RESPONSE_ONLY_FIELDS:
        template.pop(key, None)

    containers = template.get("containerDefinitions", [])
    if len(containers) != 1:
        raise ValueError("expected exactly one container in the server task definition")

    server = copy.deepcopy(template)
    server["containerDefinitions"][0]["image"] = image

    migration = copy.deepcopy(server)
    migration["family"] = migrate_family
    container = migration["containerDefinitions"][0]
    container["entryPoint"] = ["/migrate"]
    container["command"] = []
    container["portMappings"] = []
    container.pop("healthCheck", None)
    container.pop("environmentFiles", None)

    container["environment"] = [
        item
        for item in container.get("environment", [])
        if item.get("name", "").startswith("COMPLIARY_DATABASE_")
    ]
    environment_names = {item.get("name") for item in container["environment"]}
    missing_environment = sorted(REQUIRED_DATABASE_ENV - environment_names)
    if missing_environment:
        raise ValueError(
            "migration task is missing database environment: "
            + ", ".join(missing_environment)
        )

    container["secrets"] = [
        item
        for item in container.get("secrets", [])
        if item.get("name") == "COMPLIARY_DATABASE_PASSWORD"
    ]
    if len(container["secrets"]) != 1:
        raise ValueError("migration task requires COMPLIARY_DATABASE_PASSWORD")

    return server, migration


def main() -> None:
    parser = argparse.ArgumentParser()
    parser.add_argument("--base", required=True, type=Path)
    parser.add_argument("--server-out", required=True, type=Path)
    parser.add_argument("--migrate-out", required=True, type=Path)
    parser.add_argument("--image", required=True)
    parser.add_argument("--migrate-family", required=True)
    args = parser.parse_args()

    with args.base.open(encoding="utf-8") as source:
        base = json.load(source)
    server, migration = render(base, args.image, args.migrate_family)

    with args.server_out.open("w", encoding="utf-8") as destination:
        json.dump(server, destination)
    with args.migrate_out.open("w", encoding="utf-8") as destination:
        json.dump(migration, destination)


if __name__ == "__main__":
    main()
