/**
 * Validates critical cryptographic environment variables on startup.
 * Throws a fatal error if peppers are missing in non-test environments.
 */
export function validateEnvironmentPeppers() {
  const isTest = process.env.NODE_ENV === 'test';
  const walletPepper = process.env.DEODES_WALLET_PEPPER;
  const passkeyPepper = process.env.DEODES_PASSKEY_PEPPER;

  if (!walletPepper || !passkeyPepper) {
    if (isTest) {
      // Allow fallback strictly during automated unit/integration test suites
      process.env.DEODES_WALLET_PEPPER = walletPepper || 'test-wallet-pepper-only';
      process.env.DEODES_PASSKEY_PEPPER = passkeyPepper || 'test-passkey-pepper-only';
      return;
    }

    const missing = [];
    if (!walletPepper) missing.push('DEODES_WALLET_PEPPER');
    if (!passkeyPepper) missing.push('DEODES_PASSKEY_PEPPER');

    throw new Error(
      `[SECURITY CRITICAL] Fatal Startup Error: Missing pepper environment variable(s): ${missing.join(
        ', '
      )}. Hardcoded fallbacks are strictly prohibited to prevent deterministic key derivation.`
    );
  }
}