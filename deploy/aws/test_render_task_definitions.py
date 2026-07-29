import copy
import unittest

from render_task_definitions import render


class RenderTaskDefinitionsTest(unittest.TestCase):
    def setUp(self) -> None:
        self.base = {
            "family": "compliary-mcp",
            "taskDefinitionArn": "arn:old",
            "revision": 23,
            "status": "ACTIVE",
            "requiresAttributes": [],
            "compatibilities": ["EC2"],
            "registeredAt": "now",
            "registeredBy": "operator",
            "networkMode": "host",
            "containerDefinitions": [
                {
                    "name": "compliary-mcp",
                    "image": "old:latest",
                    "entryPoint": ["/server"],
                    "portMappings": [{"containerPort": 8084, "hostPort": 8084}],
                    "healthCheck": {"command": ["CMD", "/healthcheck"]},
                    "environmentFiles": [{"type": "s3", "value": "arn:file"}],
                    "environment": [
                        {"name": "PORT", "value": "8084"},
                        {"name": "COMPLIARY_DATABASE_HOST", "value": "db"},
                        {"name": "COMPLIARY_DATABASE_PORT", "value": "5432"},
                        {"name": "COMPLIARY_DATABASE_USER", "value": "compliary"},
                        {"name": "COMPLIARY_DATABASE_NAME", "value": "compliary"},
                        {"name": "COMPLIARY_DATABASE_SSLMODE", "value": "require"},
                        {"name": "COMPLIARY_PUBLIC_URL", "value": "https://example.com"},
                    ],
                    "secrets": [
                        {
                            "name": "COMPLIARY_DATABASE_PASSWORD",
                            "valueFrom": "arn:db-password",
                        },
                        {"name": "COMPLIARY_MCP_TOKEN", "valueFrom": "arn:mcp-token"},
                    ],
                }
            ],
        }
        self.image = "example.com/compliary@sha256:" + ("a" * 64)

    def test_renders_pinned_server_and_db_only_migration(self) -> None:
        original = copy.deepcopy(self.base)
        server, migration = render(
            self.base,
            self.image,
            "compliary-mcp-migrate",
        )

        self.assertEqual(self.base, original)
        self.assertEqual(server["family"], "compliary-mcp")
        self.assertEqual(server["containerDefinitions"][0]["image"], self.image)
        self.assertNotIn("taskDefinitionArn", server)
        self.assertNotIn("revision", server)

        self.assertEqual(migration["family"], "compliary-mcp-migrate")
        container = migration["containerDefinitions"][0]
        self.assertEqual(container["entryPoint"], ["/migrate"])
        self.assertEqual(container["command"], [])
        self.assertEqual(container["portMappings"], [])
        self.assertNotIn("healthCheck", container)
        self.assertNotIn("environmentFiles", container)
        self.assertEqual(
            {item["name"] for item in container["environment"]},
            {
                "COMPLIARY_DATABASE_HOST",
                "COMPLIARY_DATABASE_PORT",
                "COMPLIARY_DATABASE_USER",
                "COMPLIARY_DATABASE_NAME",
                "COMPLIARY_DATABASE_SSLMODE",
            },
        )
        self.assertEqual(
            [item["name"] for item in container["secrets"]],
            ["COMPLIARY_DATABASE_PASSWORD"],
        )

    def test_requires_digest_pinned_image(self) -> None:
        with self.assertRaisesRegex(ValueError, "sha256"):
            render(self.base, "example.com/compliary:latest", "migration")

    def test_requires_database_password_secret(self) -> None:
        self.base["containerDefinitions"][0]["secrets"] = []
        with self.assertRaisesRegex(ValueError, "DATABASE_PASSWORD"):
            render(self.base, self.image, "migration")

    def test_rejects_multi_container_definition(self) -> None:
        self.base["containerDefinitions"].append({"name": "sidecar"})
        with self.assertRaisesRegex(ValueError, "exactly one"):
            render(self.base, self.image, "migration")


if __name__ == "__main__":
    unittest.main()
