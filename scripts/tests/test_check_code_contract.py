"""Transport-adapter import-contract oracles (ADR-007; PR gate for the amended import contract)."""
from pathlib import Path
import shutil
import subprocess
import tempfile
import unittest

ROOT = Path(__file__).resolve().parents[2]
BAD = 'package fixture\ntype record struct{}\n'
GOOD = 'package fixture\nfunc work(){}\n'


class TransportAdapterContract(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        (self.root / 'scripts').mkdir()
        for name in ['contract_sources.py', 'check_code_contract.py']:
            shutil.copy(ROOT / 'scripts' / name, self.root / 'scripts')
        shutil.copytree(ROOT / 'scripts/code-contract', self.root / 'scripts/code-contract', ignore=shutil.ignore_patterns('*_test.go'))
        (self.root / 'go.mod').write_text('module fixture\n\ngo 1.26.0\n')
        (self.root / 'scripts/fixture').mkdir()
        (self.root / 'scripts/fixture/legacy.go').write_text(BAD)
        self.git('init', '-q')
        self.git('config', 'user.name', 'Synthetic contract fixture')
        self.git('config', 'user.email', 'fixture@example.invalid')
        self.git('config', 'commit.gpgsign', 'false')
        self.commit()
        self.base = self.git('rev-parse', 'HEAD').strip()

    def git(self, *args):
        return subprocess.check_output(['git', *args], cwd=self.root, stderr=subprocess.STDOUT).decode()

    def commit(self):
        self.git('add', '.')
        self.git('commit', '-qm', 'synthetic contract fixture')

    def guard(self):
        return subprocess.run(['python3', '-B', 'scripts/check_code_contract.py', '--base', self.base], cwd=self.root, capture_output=True, text=True).returncode

    def test_domain_net_http_ban_is_unchanged(self):
        path = self.root / 'internal/runtime/serve.go'
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_text('package runtime\nimport _ "net/http"\n')
        self.commit()
        self.assertNotEqual(self.guard(), 0)

    def test_cli_imports_server_adapter(self):
        path = self.root / 'cmd/flowstate/serve.go'
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_text('package main\nimport _ "github.com/baphled/flowstate/internal/server"\n')
        self.commit()
        self.assertEqual(self.guard(), 0)

    def test_server_adapter_imports_net_http(self):
        path = self.root / 'internal/server/serve.go'
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_text('package server\nimport _ "net/http"\n')
        self.commit()
        self.assertEqual(self.guard(), 0)

    def test_cli_cannot_import_net_http(self):
        path = self.root / 'cmd/flowstate/serve.go'
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_text('package main\nimport _ "net/http"\n')
        self.commit()
        self.assertNotEqual(self.guard(), 0)

    def test_server_adapter_cannot_import_app(self):
        path = self.root / 'internal/server/serve.go'
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_text('package server\nimport _ "github.com/baphled/flowstate/internal/app"\n')
        self.commit()
        self.assertNotEqual(self.guard(), 0)

    def test_server_adapter_cannot_import_command(self):
        path = self.root / 'internal/server/routes.go'
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_text('package server\nimport _ "github.com/baphled/flowstate/cmd/flowstate"\n')
        self.commit()
        self.assertNotEqual(self.guard(), 0)

    def test_app_imports_domain_without_adapter_classification(self):
        path = self.root / 'internal/app/wire.go'
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_text('package app\nimport _ "github.com/baphled/flowstate/internal/session"\n')
        self.commit()
        self.assertEqual(self.guard(), 0)
