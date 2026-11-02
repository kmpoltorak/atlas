"""Render Atlas's actual startup configuration without creating containers."""
import json
import os
from pathlib import Path
import subprocess
import unittest

ROOT = Path(__file__).resolve().parent.parent


class SharedMonitoring(unittest.TestCase):
    def test_default_uses_shared_monitoring_and_preserves_application_storage(self):
        result = subprocess.run(['docker', 'compose', '--env-file', '/dev/null', 'config',
                                 '--format', 'json'], cwd=ROOT, text=True,
                                capture_output=True, check=True, timeout=30)
        model = json.loads(result.stdout)
        services = model['services']
        self.assertEqual(set(services), {'atlas-api', 'atlas-trainer', 'timescaledb', 'redis'})
        for name in ['atlas-api', 'atlas-trainer']:
            self.assertEqual(set(services[name]['networks']), {'default', 'monitoring'})
        self.assertEqual(services['atlas-api']['ports'][0]['host_ip'], '127.0.0.1')
        self.assertEqual(services['atlas-api']['ports'][0]['published'], '8080')
        self.assertTrue(model['networks']['monitoring'].get('external', False))
        self.assertEqual(model['networks']['monitoring']['name'], 'kmpoltorak-monitoring-atlas')
        self.assertEqual(set(model['volumes']), {'timescaledb-data', 'redis-data', 'trainer-gomod-cache'})
        self.assertNotIn('traefik', model['networks'])
        self.assertNotIn('traefik.enable', services['atlas-api'].get('labels', {}))

    def test_optional_traefik_routes_only_api_with_tls_and_auth(self):
        result = subprocess.run(
            ['docker', 'compose', '--env-file', '/dev/null', '-f', 'docker-compose.yml',
             '-f', 'docker-compose.traefik.yml', 'config', '--format', 'json'],
            cwd=ROOT, env={**os.environ, 'DOMAIN_NAME': 'example.com'},
            text=True, capture_output=True, check=True, timeout=30)
        model = json.loads(result.stdout)
        services = model['services']
        api = services['atlas-api']
        self.assertEqual(set(api['networks']), {'default', 'monitoring', 'traefik'})
        self.assertTrue(model['networks']['traefik']['external'])
        self.assertEqual(model['networks']['traefik']['name'], 'hometools_default')
        self.assertEqual(api['ports'][0]['host_ip'], '127.0.0.1')
        labels = api['labels']
        self.assertEqual(labels['traefik.enable'], 'true')
        self.assertEqual(labels['traefik.docker.network'], 'hometools_default')
        self.assertEqual(labels['traefik.http.routers.atlas.rule'], 'Host(`atlas.example.com`)')
        self.assertEqual(labels['traefik.http.routers.atlas.entrypoints'], 'websecure')
        self.assertEqual(labels['traefik.http.routers.atlas.tls'], 'true')
        self.assertEqual(labels['traefik.http.routers.atlas.tls.certresolver'], 'myresolver')
        self.assertEqual(labels['traefik.http.routers.atlas.middlewares'],
                         'admin-auth@docker,security-headers@docker')
        self.assertEqual(labels['traefik.http.routers.atlas.service'], 'atlas')
        self.assertEqual(labels['traefik.http.services.atlas.loadbalancer.server.port'], '8080')
        for name in ['atlas-trainer', 'timescaledb', 'redis']:
            self.assertNotIn('traefik', services[name]['networks'])
            self.assertNotIn('traefik.enable', services[name].get('labels', {}))

    def test_optional_traefik_rejects_missing_or_empty_domain(self):
        for domain in [None, '']:
            with self.subTest(domain=domain):
                env = {key: value for key, value in os.environ.items() if key != 'DOMAIN_NAME'}
                if domain is not None:
                    env['DOMAIN_NAME'] = domain
                result = subprocess.run(
                    ['docker', 'compose', '--env-file', '/dev/null', '-f', 'docker-compose.yml',
                     '-f', 'docker-compose.traefik.yml', 'config', '--format', 'json'],
                    cwd=ROOT, env=env, text=True, capture_output=True, timeout=30)
                self.assertNotEqual(result.returncode, 0)
                self.assertIn('set DOMAIN_NAME in .env', result.stderr)
