package controller

import (
	"errors"
	"fmt"
	"net/http"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting"
	"github.com/QuantumNous/new-api/setting/billing_setting"
	"github.com/QuantumNous/new-api/setting/console_setting"
	"github.com/QuantumNous/new-api/setting/model_setting"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/QuantumNous/new-api/setting/request_plugin_setting"
	"github.com/QuantumNous/new-api/setting/system_setting"

	"github.com/gin-gonic/gin"
)

var completionRatioMetaOptionKeys = []string{
	"ModelPrice",
	"ModelRatio",
	"CompletionRatio",
	"CacheRatio",
	"CreateCacheRatio",
	"ImageRatio",
	"AudioRatio",
	"AudioCompletionRatio",
}

func isPaymentComplianceOptionKey(key string) bool {
	return strings.HasPrefix(key, "payment_setting.compliance_")
}

func isPositiveOptionValue(value string) bool {
	intValue, err := strconv.Atoi(strings.TrimSpace(value))
	if err == nil {
		return intValue > 0
	}
	floatValue, err := strconv.ParseFloat(strings.TrimSpace(value), 64)
	return err == nil && floatValue > 0
}

func collectModelNamesFromOptionValue(raw string, modelNames map[string]struct{}) {
	if strings.TrimSpace(raw) == "" {
		return
	}

	var parsed map[string]any
	if err := common.UnmarshalJsonStr(raw, &parsed); err != nil {
		return
	}

	for modelName := range parsed {
		modelNames[modelName] = struct{}{}
	}
}

func buildCompletionRatioMetaValue(optionValues map[string]string) string {
	modelNames := make(map[string]struct{})
	for _, key := range completionRatioMetaOptionKeys {
		collectModelNamesFromOptionValue(optionValues[key], modelNames)
	}

	meta := make(map[string]ratio_setting.CompletionRatioInfo, len(modelNames))
	for modelName := range modelNames {
		meta[modelName] = ratio_setting.GetCompletionRatioInfo(modelName)
	}

	jsonBytes, err := common.Marshal(meta)
	if err != nil {
		return "{}"
	}
	return string(jsonBytes)
}

func GetOptions(c *gin.Context) {
	var options []*model.Option
	optionValues := make(map[string]string)
	common.OptionMapRWMutex.Lock()
	for k, v := range common.OptionMap {
		if model.IsLoginVerificationUserOption(k) {
			continue
		}
		if k == "theme.frontend" || k == "billing_setting.billing_mode" || k == "billing_setting.billing_expr" || k == "ResendKeyConfigured" {
			continue
		}
		value := common.Interface2String(v)
		isSensitiveKey := strings.HasSuffix(k, "Token") ||
			strings.HasSuffix(k, "Secret") ||
			strings.HasSuffix(k, "Key") ||
			strings.HasSuffix(k, "secret") ||
			strings.HasSuffix(k, "api_key") ||
			strings.HasSuffix(k, "private_key")
		if isSensitiveKey {
			continue
		}
		options = append(options, &model.Option{
			Key:   k,
			Value: value,
		})
		if slices.Contains(completionRatioMetaOptionKeys, k) {
			optionValues[k] = value
		}
	}
	options = append(options, &model.Option{
		Key: "ResendKeyConfigured", Value: strconv.FormatBool(strings.TrimSpace(common.OptionMap["ResendApiKey"]) != ""),
	})
	common.OptionMapRWMutex.Unlock()
	// Display the same effective expressions used by pricing and settlement,
	// including built-in defaults absent from persisted administrator options.
	for key, values := range map[string]map[string]string{
		"billing_setting.billing_mode": billing_setting.GetBillingModeCopy(),
		"billing_setting.billing_expr": billing_setting.GetBillingExprCopy(),
	} {
		encoded, err := common.Marshal(values)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": err.Error()})
			return
		}
		options = append(options, &model.Option{Key: key, Value: string(encoded)})
	}
	options = append(options, &model.Option{
		Key:   "CompletionRatioMeta",
		Value: buildCompletionRatioMetaValue(optionValues),
	})
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data":    options,
	})
}

type OptionUpdateRequest struct {
	Key   string `json:"key"`
	Value any    `json:"value"`
}

func UpdatePasskeyDomains(c *gin.Context) {
	var request struct {
		RPID                *string `json:"rp_id"`
		LegacyRPIDs         *string `json:"legacy_rp_ids"`
		Origins             *string `json:"origins"`
		Preview             bool    `json:"preview"`
		RemovalConfirmation string  `json:"removal_confirmation"`
	}
	if err := common.DecodeJson(c.Request.Body, &request); err != nil || request.RPID == nil || request.LegacyRPIDs == nil || request.Origins == nil {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	change, err := model.UpdatePasskeyDomainOptions(map[string]string{
		"passkey.rp_id": *request.RPID, "passkey.legacy_rp_ids": *request.LegacyRPIDs, "passkey.origins": *request.Origins,
	}, request.Preview, request.RemovalConfirmation)
	if err != nil {
		writePasskeyDomainSettingsError(c, err)
		if !request.Preview {
			recordPasskeyDomainAudit(c, change, request.RemovalConfirmation != "", err)
		}
		return
	}
	if !request.Preview {
		recordPasskeyDomainAudit(c, change, request.RemovalConfirmation != "", nil)
	}
	common.ApiSuccess(c, change)
}

func writePasskeyDomainSettingsError(c *gin.Context, err error) {
	var removal *model.PasskeyDomainRemovalError
	if errors.As(err, &removal) {
		common.ApiErrorStatus(c, http.StatusConflict, common.NewMessage("Review the affected Passkeys and confirm the domain removal. If the settings or impact have changed, confirmation is required again."), gin.H{
			"code": "PASSKEY_RP_ID_REMOVAL_CONFIRMATION_REQUIRED",
			"data": removal.Change,
		})
		return
	}
	if errors.Is(err, system_setting.ErrPasskeyRPIDInvalid) {
		writeSecurityOperationError(c, err)
		return
	}
	common.ApiError(c, err)
}

var errTelegramOptionNotConfigured = common.NewMessage("Telegram OAuth is not configured or enabled. Please contact your administrator.")

// Both entry points validate without publishing a draft into runtime globals.
// Dependencies are read from the complete proposed section, not request order.
func validateOptionUpdate(option *OptionUpdateRequest, updates map[string]string) error {
	switch option.Value.(type) {
	case bool, float64, int:
		option.Value = common.Interface2String(option.Value)
	default:
		option.Value = fmt.Sprint(option.Value)
	}
	value := option.Value.(string)
	if (option.Key == "InviteRegisterEnabled" || option.Key == "RegisterAffiliateLinkEnabled" || option.Key == "LoginVerificationRequiredEnabled") && value != "true" && value != "false" {
		return common.NewMessage("Invalid parameters")
	}
	if model.IsLoginVerificationUserOption(option.Key) {
		return common.NewMessage("Invalid parameters")
	}
	switch option.Key {
	case "QuotaForInviter", "QuotaForInvitee":
		if isPositiveOptionValue(value) && !operation_setting.IsPaymentComplianceConfirmed() {
			return common.NewMessage("Payment, redemption, subscription, and invitation reward features are disabled. The administrator must confirm compliance terms before enabling them.")
		}
	default:
		if isPaymentComplianceOptionKey(option.Key) {
			return common.NewMessage("Compliance confirmation fields cannot be changed through the general settings API")
		}
		if option.Key == request_plugin_setting.InstancesOption {
			return common.NewMessage("Request plugin instances cannot be changed through the general settings API")
		}
	}
	if option.Key == "TaskPublicAddress" && value != "" {
		if err := service.ValidateTaskArtifactBaseURL(value); err != nil {
			return err
		}
	}

	common.OptionMapRWMutex.RLock()
	effective := map[string]string{
		"GitHubClientId":         common.GitHubClientId,
		"discord.client_id":      system_setting.GetDiscordSettings().ClientId,
		"oidc.client_id":         system_setting.GetOIDCSettings().ClientId,
		"LinuxDOClientId":        common.LinuxDOClientId,
		"EmailDomainWhitelist":   strings.Join(common.EmailDomainWhitelist, ","),
		"WeChatServerAddress":    common.WeChatServerAddress,
		"TurnstileSiteKey":       common.TurnstileSiteKey,
		"telegram.client_id":     system_setting.GetTelegramSettings().ClientID,
		"telegram.client_secret": system_setting.GetTelegramSettings().ClientSecret,
	}
	common.OptionMapRWMutex.RUnlock()
	for key := range effective {
		if proposed, changed := updates[key]; changed {
			effective[key] = proposed
		}
	}
	var err error
	switch option.Key {
	case "GitHubOAuthEnabled", "discord.enabled", "oidc.enabled", "LinuxDOOAuthEnabled":
		providers := map[string][2]string{
			"GitHubOAuthEnabled":  {"GitHub OAuth", "GitHubClientId"},
			"discord.enabled":     {"Discord OAuth", "discord.client_id"},
			"oidc.enabled":        {"OIDC", "oidc.client_id"},
			"LinuxDOOAuthEnabled": {"LinuxDO OAuth", "LinuxDOClientId"},
		}
		provider := providers[option.Key]
		if value == "true" && effective[provider[1]] == "" {
			return common.NewMessage("Cannot enable {{provider}}. Enter the {{provider}} Client ID and Client Secret first.", map[string]any{"provider": provider[0]})
		}
	case "EmailDomainRestrictionEnabled":
		if value == "true" && effective["EmailDomainWhitelist"] == "" {
			return common.NewMessage("Cannot enable the email domain restriction. Enter the allowed email domains first.")
		}
	case "WeChatAuthEnabled":
		if value == "true" && effective["WeChatServerAddress"] == "" {
			return common.NewMessage("Cannot enable WeChat login. Enter the WeChat login settings first.")
		}
	case "TurnstileCheckEnabled":
		if value == "true" && effective["TurnstileSiteKey"] == "" {
			return common.NewMessage("Cannot enable Turnstile verification. Enter the Turnstile settings first.")
		}
	case "TelegramOAuthEnabled":
		if value == "true" && (strings.TrimSpace(effective["telegram.client_id"]) == "" || strings.TrimSpace(effective["telegram.client_secret"]) == "") {
			return errTelegramOptionNotConfigured
		}
	case "theme.frontend":
		if value != "default" {
			return common.NewMessage("The Classic frontend has been removed. The theme can only be set to default")
		}
	case "GroupRatio":
		return ratio_setting.CheckGroupRatio(value)
	case "gemini.safety_settings":
		return model_setting.ValidateGeminiSafetySettings(value)
	case "claude.default_max_tokens":
		return model_setting.ValidateClaudeDefaultMaxTokens(value)
	case operation_setting.ToolPriceOptionKey:
		return operation_setting.ValidateToolPricesJSON(value)
	case "ImageRatio", "AudioRatio", "AudioCompletionRatio", "CreateCacheRatio":
		var ratios map[string]float64
		if err = common.UnmarshalJsonStr(value, &ratios); err != nil {
			messages := map[string]string{
				"ImageRatio":           "Failed to set the image ratio: {{error}}",
				"AudioRatio":           "Failed to set the audio ratio: {{error}}",
				"AudioCompletionRatio": "Failed to set the audio completion ratio: {{error}}",
				"CreateCacheRatio":     "Failed to set the cache creation ratio: {{error}}",
			}
			return common.NewMessage(messages[option.Key], map[string]any{"error": err.Error()})
		}
	case "ModelRequestRateLimitGroup":
		return setting.CheckModelRequestRateLimitGroup(value)
	case "AutomaticDisableStatusCodes", "AutomaticRetryStatusCodes":
		_, err = operation_setting.ParseHTTPStatusCodeRanges(value)
		return err
	case "billing_setting.billing_expr":
		expressions := make(map[string]string)
		if err = common.UnmarshalJsonStr(value, &expressions); err != nil {
			return common.NewMessage("Billing expressions must be a JSON object mapping models to expressions: {{error}}", map[string]any{"error": err.Error()})
		}
		models := make([]string, 0, len(expressions))
		for modelName := range expressions {
			models = append(models, modelName)
		}
		sort.Strings(models)
		storedVariants := billing_setting.GetPluginBillingExprCopy()
		for _, modelName := range models {
			variants := make(map[string]any)
			for key, expression := range storedVariants {
				if plugin, name, ok := billing_setting.SplitPluginBillingExprKey(key); ok && name == modelName {
					variants[plugin] = expression
				}
			}
			if err = model.ValidateModelPricing(modelName, model.PricingValues{
				"billing_setting.billing_expr":          expressions[modelName],
				billing_setting.PluginBillingExprOption: variants,
			}); err != nil {
				return common.NewMessage("Invalid billing expression for model {{model}}: {{error}}", map[string]any{"model": modelName, "error": err.Error()})
			}
		}
	case billing_setting.PluginBillingExprOption:
		var expressions map[string]string
		if err = common.UnmarshalJsonStr(value, &expressions); err != nil || expressions == nil {
			return errors.New("plugin billing expressions must be a JSON object")
		}
		for key, expression := range expressions {
			plugin, name, valid := billing_setting.SplitPluginBillingExprKey(key)
			if !valid {
				return fmt.Errorf("invalid plugin billing expression key: %s", key)
			}
			if err = model.ValidateModelPricing(name, model.PricingValues{
				billing_setting.PluginBillingExprOption: map[string]any{plugin: expression},
			}); err != nil {
				return err
			}
		}
	case "console_setting.api_info":
		return console_setting.ValidateConsoleSettings(value, "ApiInfo")
	case "console_setting.announcements":
		return console_setting.ValidateConsoleSettings(value, "Announcements")
	case "console_setting.faq":
		return console_setting.ValidateConsoleSettings(value, "FAQ")
	case "console_setting.uptime_kuma_groups":
		return console_setting.ValidateConsoleSettings(value, "UptimeKumaGroups")
	}
	return nil
}

// UpdateOptionsBatch shares the existing option route's root/session/PAT guards.
// It never dispatches specialized keys through the ordinary bulk store.
func UpdateOptionsBatch(c *gin.Context) {
	var request struct {
		Options []OptionUpdateRequest `json:"options"`
	}
	if err := common.DecodeJson(c.Request.Body, &request); err != nil || len(request.Options) == 0 || len(request.Options) > 100 {
		common.ApiErrorStatus(c, http.StatusBadRequest, common.NewMessage("Invalid parameters"))
		return
	}
	values := make(map[string]string, len(request.Options))
	for i := range request.Options {
		option := &request.Options[i]
		if strings.TrimSpace(option.Key) == "" || len(option.Key) > 191 {
			common.ApiErrorStatus(c, http.StatusBadRequest, common.NewMessage("Invalid parameters"), gin.H{"key": option.Key})
			return
		}
		if _, duplicate := values[option.Key]; duplicate {
			common.ApiErrorStatus(c, http.StatusBadRequest, common.NewMessage("Invalid parameters"), gin.H{"key": option.Key})
			return
		}
		switch option.Value.(type) {
		case string, bool, float64:
		default:
			common.ApiErrorStatus(c, http.StatusBadRequest, common.NewMessage("Invalid parameters"), gin.H{"key": option.Key})
			return
		}
		if model.IsPasskeyDomainOption(option.Key) || model.IsModelPricingOption(option.Key) || model.IsRequestPolicyOption(option.Key) {
			common.ApiErrorStatus(c, http.StatusBadRequest, common.NewMessage("This setting must be saved through its dedicated settings API"), gin.H{"key": option.Key})
			return
		}
		option.Value = common.Interface2String(option.Value)
		values[option.Key] = option.Value.(string)
	}
	options := make([]model.Option, 0, len(request.Options))
	keys := make([]string, 0, len(request.Options))
	for i := range request.Options {
		option := &request.Options[i]
		if err := validateOptionUpdate(option, values); err != nil {
			fields := gin.H{"key": option.Key}
			if errors.Is(err, errTelegramOptionNotConfigured) {
				fields["code"] = "TELEGRAM_OAUTH_NOT_CONFIGURED"
			}
			common.ApiErrorStatus(c, http.StatusOK, err, fields)
			return
		}
		options = append(options, model.Option{Key: option.Key, Value: option.Value.(string)})
		keys = append(keys, option.Key)
	}
	// Preserve the existing translated audit action; key is display text and
	// keys is structured context. Neither includes any setting value.
	draft := buildAuditDraft(snapshotAuditActor(c), "option.update", map[string]any{
		"key": strings.Join(keys, ", "), "keys": keys,
	})
	if err := model.UpdateOrdinaryOptionsBatch(c.Request.Context(), options, draft); err != nil {
		fields := gin.H{}
		var optionError *model.OptionUpdateError
		if errors.As(err, &optionError) {
			fields["key"] = optionError.Key
		}
		// Driver errors, including a failed commit, can contain secret values.
		var message *common.Message
		if !errors.As(err, &message) {
			err = common.NewMessage("Failed to update settings")
		}
		common.ApiErrorStatus(c, http.StatusOK, err, fields)
		return
	}
	markAuditLogged(c)
	common.ApiSuccess(c, nil)
}

func UpdateOption(c *gin.Context) {
	var option OptionUpdateRequest
	if err := common.DecodeJson(c.Request.Body, &option); err != nil {
		common.ApiErrorStatus(c, http.StatusBadRequest, common.NewMessage("Invalid parameters"))
		return
	}
	if err := validateOptionUpdate(&option, nil); err != nil {
		fields := gin.H{}
		if errors.Is(err, errTelegramOptionNotConfigured) {
			fields["code"] = "TELEGRAM_OAUTH_NOT_CONFIGURED"
		}
		common.ApiErrorStatus(c, http.StatusOK, err, fields)
		return
	}
	if model.IsPasskeyDomainOption(option.Key) {
		change, updateErr := model.UpdatePasskeyDomainOptions(map[string]string{option.Key: option.Value.(string)}, false, "")
		if updateErr != nil {
			writePasskeyDomainSettingsError(c, updateErr)
			recordPasskeyDomainAudit(c, change, false, updateErr)
			return
		}
		recordPasskeyDomainAudit(c, change, false, nil)
		common.ApiSuccess(c, change)
		return
	}
	ordinary := !model.IsModelPricingOption(option.Key) && !model.IsRequestPolicyOption(option.Key)
	var err error
	if ordinary {
		// Single-field saves need the same atomic audit and sync obligations as
		// section saves. Otherwise the response waits for two more independent
		// commits after the setting is already stored, even after cancellation.
		draft := buildAuditDraft(snapshotAuditActor(c), "option.update", map[string]any{"key": option.Key})
		err = model.UpdateOrdinaryOptionsBatch(c.Request.Context(), []model.Option{{Key: option.Key, Value: option.Value.(string)}}, draft)
		var message *common.Message
		if err != nil && !errors.As(err, &message) {
			err = common.NewMessage("Failed to update settings")
		}
	} else {
		err = model.UpdateOption(option.Key, option.Value.(string))
	}
	if err != nil {
		if errors.Is(err, system_setting.ErrPasskeyRPIDInvalid) {
			writeSecurityOperationError(c, err)
		} else {
			common.ApiError(c, err)
		}
		return
	}
	if ordinary {
		markAuditLogged(c)
	} else {
		// 出于安全考虑只记录被修改的配置项名称，不记录配置值（可能含密钥等敏感信息）。
		recordManageAudit(c, "option.update", map[string]any{
			"key": option.Key,
		})
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
	})
}
