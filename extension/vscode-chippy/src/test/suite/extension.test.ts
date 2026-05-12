import * as assert from 'assert';
import * as vscode from 'vscode';

// Smoke tests that catch the most common manifest / activation regressions:
// a typo in `contributes.debuggers[].type`, a removed activation event,
// or a broken DebugAdapterDescriptorFactory factory function.

const EXT_ID = 'nkane.vscode-chippy';

suite('vscode-chippy', () => {
  test('extension is present', () => {
    const ext = vscode.extensions.getExtension(EXT_ID);
    assert.ok(ext, `expected extension ${EXT_ID} to be present`);
  });

  test('extension activates on debug-type resolve', async () => {
    const ext = vscode.extensions.getExtension(EXT_ID);
    assert.ok(ext);
    if (!ext!.isActive) {
      await ext!.activate();
    }
    assert.strictEqual(ext!.isActive, true, 'extension did not activate');
  });

  test('package.json contributes the `chippy` debug type', () => {
    const ext = vscode.extensions.getExtension(EXT_ID);
    assert.ok(ext);
    const debuggers = ext!.packageJSON.contributes.debuggers as Array<{ type: string }>;
    assert.ok(Array.isArray(debuggers), 'contributes.debuggers must be an array');
    const types = debuggers.map((d) => d.type);
    assert.ok(types.includes('chippy'), `expected chippy in ${JSON.stringify(types)}`);
  });

  test('chippy.binaryPath setting is declared', () => {
    const ext = vscode.extensions.getExtension(EXT_ID);
    assert.ok(ext);
    const props = ext!.packageJSON.contributes.configuration.properties as Record<string, unknown>;
    assert.ok('chippy.binaryPath' in props, 'expected chippy.binaryPath in configuration');
  });
});
