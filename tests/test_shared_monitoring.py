"""Render Atlas's actual startup configuration without creating containers."""
import json
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
