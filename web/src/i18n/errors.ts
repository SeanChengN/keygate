import type { Locale, TranslationKeys } from "@/i18n"
import en from "./locales/en"
import zh from "./locales/zh"

const codes: Record<string, TranslationKeys> = {
  AI_GROUP_INVALID: "ai.groupInvalid",
  AI_QUOTA_INVALID: "ai.quotaInvalid",
  AI_RATE_INVALID: "ai.rateInvalid",
  AI_PLAN_INELIGIBLE: "ai.planIneligible",
  AI_PRODUCT_INVALID: "ai.planIneligible",
  AI_ACCOUNT_UNCONFIGURED: "ai.accountUnconfigured",
  AI_ACCOUNT_ATTENTION: "ai.accountAttention",
  AI_TOKENS_INVALID: "ai.tokensInvalid",
  AI_DISABLED: "ai.disabled",
  AI_CREDENTIAL_INVALID: "ai.credentialInvalid",
  AI_PROFILE_INVALID: "ai.profileInvalid",
  AI_CONFIG_CHANGED: "ai.configChanged",
  AI_RESULT_UNCERTAIN: "ai.resultUncertain",
  AI_OPERATION_BUSY: "ai.operationBusy",
  AI_REQUEST_INVALID: "ai.requestInvalid",
  INVENTORY_REQUEST_INVALID: "ai.requestInvalid",
  AI_READBACK_FAILED: "ai.readbackFailed",
  AI_PROVIDER_RATE_LIMITED: "ai.providerRateLimited",
  AI_PROVIDER_FAILED: "ai.providerFailed",
  AI_OPERATION_FAILED: "ai.operationFailed",
  AI_PROVISION_UNAVAILABLE: "ai.operationFailed",
  AI_LICENSE_UNAVAILABLE: "ai.licenseUnavailable",
  insufficient_quota: "ai.insufficientQuota",
  API_KEY_DISABLED: "ai.keyDisabled",
  API_KEY_EXPIRED: "ai.keyExpired",
  INVENTORY_CONFLICT: "inventory.conflict",
  INVENTORY_INELIGIBLE: "inventory.ineligible",
  INVENTORY_PLAN_MISMATCH: "inventory.planMismatch",
  INVENTORY_ADMIN_REQUIRED: "inventory.adminRequired",
  EXPORT_RECORD_MISSING: "inventory.recordMissing",
  NETWORK_ERROR: "errors.network",
  SESSION_EXPIRED: "errors.session",
  FORBIDDEN: "errors.forbidden",
  NOT_FOUND: "errors.notFound",
  INTERNAL_ERROR: "errors.failed",
  BAD_REQUEST: "ai.requestInvalid",
}

export function errorMessage(error: unknown, locale: Locale): string {
  const code =
    typeof error === "object" && error !== null && "code" in error && typeof error.code === "string"
      ? error.code
      : "REQUEST_FAILED"
  const dictionary = locale === "zh" ? zh : en
  return `${dictionary[codes[code] || "errors.failed"]} (${code})`
}

export function clientError(code: string, status?: number): Error {
  const error = Object.assign(new Error(), { code, status })
  const locale =
    localStorage.getItem("keygate_locale") || (navigator.language.toLowerCase().startsWith("zh") ? "zh" : "en")
  error.message = errorMessage(error, locale === "zh" ? "zh" : "en")
  return error
}
