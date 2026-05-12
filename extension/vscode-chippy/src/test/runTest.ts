import * as path from 'path';
import { runTests } from '@vscode/test-electron';

async function main(): Promise<void> {
  // The folder containing the Extension Manifest package.json.
  const extensionDevelopmentPath = path.resolve(__dirname, '../../../');
  // The path to the test runner module (compiled from src/test/suite).
  const extensionTestsPath = path.resolve(__dirname, './suite/index');

  try {
    await runTests({ extensionDevelopmentPath, extensionTestsPath });
  } catch (err) {
    console.error('Test run failed:', err);
    process.exit(1);
  }
}

main();
