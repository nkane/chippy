import * as vscode from 'vscode';

export function activate(context: vscode.ExtensionContext): void {
  const factory = new ChippyDebugAdapterDescriptorFactory();
  context.subscriptions.push(
    vscode.debug.registerDebugAdapterDescriptorFactory('chippy', factory),
  );
}

export function deactivate(): void {
  // Subscriptions teardown is handled by VS Code via context.subscriptions.
}

class ChippyDebugAdapterDescriptorFactory
  implements vscode.DebugAdapterDescriptorFactory
{
  createDebugAdapterDescriptor(
    _session: vscode.DebugSession,
    _executable: vscode.DebugAdapterExecutable | undefined,
  ): vscode.DebugAdapterDescriptor {
    const config = vscode.workspace.getConfiguration('chippy');
    const binPath = config.get<string>('binaryPath') ?? 'chippy';
    return new vscode.DebugAdapterExecutable(binPath, ['-dap', 'stdio']);
  }
}
