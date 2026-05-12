import * as path from 'path';
import * as fs from 'fs';
import Mocha from 'mocha';

export function run(): Promise<void> {
  // tdd UI is the VS Code testing convention (`suite` / `test`).
  const mocha = new Mocha({ ui: 'tdd', color: true, timeout: 20000 });
  const testsRoot = __dirname;

  return new Promise((resolve, reject) => {
    const files = fs
      .readdirSync(testsRoot)
      .filter((f) => f.endsWith('.test.js'))
      .map((f) => path.resolve(testsRoot, f));
    files.forEach((f) => mocha.addFile(f));

    try {
      mocha.run((failures) => {
        if (failures > 0) {
          reject(new Error(`${failures} tests failed`));
        } else {
          resolve();
        }
      });
    } catch (err) {
      reject(err);
    }
  });
}
