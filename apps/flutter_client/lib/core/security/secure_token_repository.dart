// Local credential storage boundary.
//
// The auth token must never live in the shared settings JSON (which is
// non-credentials config that can be backed up, shipped in support bundles,
// or read by other local processes), and it must not be passed to the daemon
// on the process command line. `SecureTokenRepository` isolates that storage
// behind an interface so:
//   - production uses an app-local token file;
//   - tests inject an in-memory implementation;
//   - business code never knows the concrete platform mechanism.

import 'dart:io';

import 'package:flutter_secure_storage_windows/flutter_secure_storage_windows.dart';

import '../platform/android_platform.dart';

abstract interface class SecureTokenRepository {
  /// Read the stored token, or `null` when none is stored.
  Future<String?> read();

  /// Persist [token] (trimmed). Replaces any prior value.
  Future<void> write(String token);

  /// Remove the stored token, if any.
  Future<void> clear();
}

class SecureTokenStorageException implements Exception {
  const SecureTokenStorageException(this.message);

  final String message;

  @override
  String toString() => message;
}

/// Production repository backed by an app-local file.
///
/// This intentionally does not use Keychain, Keystore, Secret Service, or
/// another OS credential broker: those services can show an interactive
/// system-password prompt on desktop. On POSIX systems the directory and file
/// are restricted to the current user (`0700`/`0600`). Windows app-data is
/// already scoped to the current user by the OS profile.
class LocalTokenRepository implements SecureTokenRepository {
  LocalTokenRepository({
    File? file,
    this.legacyWindowsTokenReader,
    this.legacyWindowsTokenClearer,
    bool? isWindows,
  }) : _fileOverride = file,
       _isWindows = isWindows ?? Platform.isWindows;

  static const _fileName = 'p2wlan-auth-token';
  static const _legacyWindowsTokenKey = 'p2wlan.control.auth_token';
  static const _legacyWindowsStorageOptions = {
    'useBackwardCompatibility': 'true',
  };
  final File? _fileOverride;
  final LegacyWindowsTokenReader? legacyWindowsTokenReader;
  final LegacyWindowsTokenClearer? legacyWindowsTokenClearer;
  final bool _isWindows;

  File _tokenFileSync() {
    final override = _fileOverride;
    if (override != null) return override;

    final directory = _localDirectorySync();
    return File('${directory.path}${Platform.pathSeparator}$_fileName');
  }

  Future<File> _tokenFile() async {
    final override = _fileOverride;
    if (override != null) return override;
    final applicationSupport = await resolveApplicationSupportDirectory();
    if (applicationSupport == null) {
      throw StateError('Android application support directory is unavailable.');
    }
    return File(
      '${applicationSupport.path}${Platform.pathSeparator}$_fileName',
    );
  }

  Directory _localDirectorySync() {
    if (Platform.isMacOS || Platform.isIOS) {
      final home = Platform.environment['HOME'];
      if (home != null && home.isNotEmpty) {
        return Directory('$home/Library/Application Support/p2wlan-client');
      }
    }
    if (Platform.isWindows) {
      final appData = Platform.environment['APPDATA'];
      if (appData != null && appData.isNotEmpty) {
        return Directory('$appData\\p2wlan');
      }
    }
    if (Platform.isLinux) {
      final xdg = Platform.environment['XDG_CONFIG_HOME'];
      if (xdg != null && xdg.isNotEmpty) {
        return Directory('$xdg/p2wlan');
      }
      final home = Platform.environment['HOME'];
      if (home != null && home.isNotEmpty) {
        return Directory('$home/.config/p2wlan');
      }
    }

    // This is retained only for unsupported/sandboxed runtimes that do not
    // expose a stable application-data directory. Android is handled above by
    // the Context-backed application support directory.
    return Directory(
      '${Directory.systemTemp.path}${Platform.pathSeparator}p2wlan-client',
    );
  }

  File? _legacyAndroidTokenFile() {
    if (!Platform.isAndroid || _fileOverride != null) return null;
    return File(
      '${Directory.systemTemp.path}${Platform.pathSeparator}p2wlan-client${Platform.pathSeparator}$_fileName',
    );
  }

  Future<void> _restrictDirectory(Directory directory) async {
    // Mobile sandboxes already isolate their application files, and spawning
    // /bin/chmod is unavailable on iOS/Android. Desktop POSIX permissions are
    // applied explicitly below.
    if (!Platform.isMacOS && !Platform.isLinux) return;
    final result = await Process.run('/bin/chmod', ['700', directory.path]);
    if (result.exitCode != 0) {
      throw const SecureTokenStorageException('无法限制本地凭据目录权限，未能保存 P2WLAN 登录凭据。');
    }
  }

  Future<void> _restrictFile(File file) async {
    if (!Platform.isMacOS && !Platform.isLinux) return;
    final result = await Process.run('/bin/chmod', ['600', file.path]);
    if (result.exitCode != 0) {
      throw const SecureTokenStorageException('无法限制本地凭据文件权限，未能保存 P2WLAN 登录凭据。');
    }
  }

  @override
  Future<String?> read() async {
    try {
      final file = Platform.isAndroid ? await _tokenFile() : _tokenFileSync();
      if (await file.exists()) {
        final token = (await file.readAsString()).trim();
        if (token.isNotEmpty) return token;
      }

      if (_isWindows &&
          (_fileOverride == null || legacyWindowsTokenReader != null)) {
        final legacy = (legacyWindowsTokenReader ?? _readLegacyWindowsToken);
        final token = (await legacy())?.trim() ?? '';
        if (token.isNotEmpty) {
          await _writeFile(file, token);
          if ((await file.readAsString()).trim() != token) {
            throw const SecureTokenStorageException('Windows 旧版登录凭据迁移校验失败。');
          }
          if (_fileOverride == null || legacyWindowsTokenClearer != null) {
            try {
              await (legacyWindowsTokenClearer ??
                  _clearLegacyWindowsToken)();
            } on Object {
              // The verified local copy remains authoritative if legacy
              // cleanup is unavailable. A later read will use that copy.
            }
          }
          return token;
        }
      }

      var source = file;
      if (!await source.exists()) {
        final legacy = _legacyAndroidTokenFile();
        if (legacy == null || !await legacy.exists()) return null;
        source = legacy;
      }
      final trimmed = (await source.readAsString()).trim();
      if (trimmed.isNotEmpty && source.path != file.path) {
        // One-time migration from the old cache/temp location used by older
        // Android builds. Keep the old file untouched so an interrupted
        // update remains recoverable.
        await _writeFile(file, trimmed);
      }
      return trimmed.isEmpty ? null : trimmed;
    } on SecureTokenStorageException {
      rethrow;
    } catch (_) {
      throw const SecureTokenStorageException('本地凭据文件不可用，无法读取 P2WLAN 登录凭据。');
    }
  }

  @override
  Future<void> write(String token) async {
    final trimmed = token.trim();
    try {
      if (trimmed.isEmpty) {
        await clear();
      } else {
        final file = Platform.isAndroid ? await _tokenFile() : _tokenFileSync();
        await _writeFile(file, trimmed);
      }
    } catch (_) {
      throw const SecureTokenStorageException(
        '本地凭据文件不可用，未能保存 P2WLAN 登录凭据；原有配置保持不变。',
      );
    }
  }

  @override
  Future<void> clear() async {
    try {
      final file = Platform.isAndroid ? await _tokenFile() : _tokenFileSync();
      if (await file.exists()) await file.delete();
      final legacy = _legacyAndroidTokenFile();
      if (legacy != null && await legacy.exists()) await legacy.delete();
      if (_isWindows &&
          (_fileOverride == null || legacyWindowsTokenClearer != null)) {
        await (legacyWindowsTokenClearer ?? _clearLegacyWindowsToken)();
      }
    } catch (_) {
      throw const SecureTokenStorageException('本地凭据文件不可用，未能删除 P2WLAN 登录凭据。');
    }
  }

  Future<String?> _readLegacyWindowsToken() async {
    try {
      final value = await FlutterSecureStorageWindows().read(
        key: _legacyWindowsTokenKey,
        options: _legacyWindowsStorageOptions,
      );
      final token = value?.trim() ?? '';
      return token.isEmpty ? null : token;
    } catch (_) {
      throw const SecureTokenStorageException('无法读取旧版 Windows 登录凭据。');
    }
  }

  Future<void> _clearLegacyWindowsToken() async {
    try {
      await FlutterSecureStorageWindows().delete(
        key: _legacyWindowsTokenKey,
        options: _legacyWindowsStorageOptions,
      );
    } catch (_) {
      throw const SecureTokenStorageException('无法清除旧版 Windows 登录凭据。');
    }
  }

  Future<void> _writeFile(File file, String token) async {
    await file.parent.create(recursive: true);
    await _restrictDirectory(file.parent);
    final temp = File(
      '${file.path}.${DateTime.now().microsecondsSinceEpoch}.tmp',
    );
    try {
      await temp.writeAsString(token, flush: true);
      await _restrictFile(temp);
      try {
        await temp.rename(file.path);
      } on FileSystemException {
        // Windows cannot replace an existing file with rename on every
        // filesystem. The temporary file is already flushed and contains
        // only the token, so this narrow fallback remains recoverable.
        if (await file.exists()) await file.delete();
        await temp.rename(file.path);
      }
      await _restrictFile(file);
    } finally {
      if (await temp.exists()) await temp.delete();
    }
  }
}

typedef LegacyWindowsTokenReader = Future<String?> Function();
typedef LegacyWindowsTokenClearer = Future<void> Function();

/// In-memory implementation for tests.
class InMemorySecureTokenRepository implements SecureTokenRepository {
  String? _token;

  @override
  Future<String?> read() async => _token;

  @override
  Future<void> write(String token) async {
    final trimmed = token.trim();
    _token = trimmed.isEmpty ? null : trimmed;
  }

  @override
  Future<void> clear() async => _token = null;
}
