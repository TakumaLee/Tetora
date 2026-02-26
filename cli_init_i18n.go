package main

// initStrings holds all user-facing strings for cmdInit, keyed by field name.
type initStrings struct {
	// Language selection
	LangPrompt string

	// Config overwrite
	ConfigExists    string
	OverwritePrompt string
	Aborted         string

	// OpenClaw migration
	OpenClawDetected     string
	OpenClawImport       string
	OpenClawImported     string
	OpenClawMigrateNote1 string
	OpenClawMigrateNote2 string

	// Main title
	Title string

	// Step 1
	Step1Title     string
	ChannelOptions [4]string

	// Telegram hints
	TelegramHint1        string
	TelegramHint2        string
	TelegramHint3        string
	TelegramHint4        string
	TelegramTokenPrompt  string
	TelegramChatIDPrompt string

	// Discord hints
	DiscordHint1         string
	DiscordHint2         string
	DiscordHint3         string
	DiscordHint4         string
	DiscordHint5         string
	DiscordHint6         string
	DiscordHint7         string
	DiscordTokenPrompt   string
	DiscordAppIDPrompt   string
	DiscordChannelPrompt string

	// Slack hints
	SlackHint1               string
	SlackHint2               string
	SlackHint3               string
	SlackTokenPrompt         string
	SlackSigningSecretPrompt string

	// Step 2
	Step2Title           string
	ProviderOptions      [3]string
	ClaudeCLIPathPrompt  string
	DefaultModelPrompt   string
	ClaudeAPIKeyPrompt   string
	OpenAIEndpointPrompt string
	OpenAIKeyPrompt      string

	// Provider hints
	ClaudeCLIHint1 string
	ClaudeCLIHint2 string
	ClaudeCLIHint3 string
	ClaudeCLIHint4 string
	ClaudeCLIHint5 string
	ClaudeAPIHint1 string
	ClaudeAPIHint2 string
	ClaudeAPIHint3 string

	// Step 3
	Step3Title     string
	Step3Note1     string
	Step3Note2     string
	DirOptions     [3]string
	DirInputPrompt string

	// Step 4
	Step4Title string

	// Agent creation
	CreateRoleOCPrompt    string
	CreateRolePrompt      string
	RoleNamePrompt        string
	ArchetypeTitle        string
	ArchetypeBlank        string
	ArchetypeChoosePrompt string
	SoulFilePrompt        string
	RoleModelPrompt       string
	RoleDescPrompt        string
	RolePermPrompt        string
	RolePermInvalid       string
	RoleAdded             string
	RoleError             string

	// Post-agent setup
	SetDefaultAgentPrompt   string
	DefaultAgentSet         string
	AutoRouteDiscordPrompt string
	AutoRouteDiscordDone   string
	AddAnotherRolePrompt   string
	EnableSmartDispatch    string
	SmartDispatchEnabled   string

	// Service install
	ServiceInstallPrompt string

	// Final summary
	FinalConfig   string
	FinalJobs     string
	NextSteps     string
	NextDoctor    string
	NextStatus    string
	NextServe     string
	NextDashboard string

	// API token
	APITokenLabel string
	APITokenNote  string
}

// initTranslations maps language codes to their initStrings.
var initTranslations = map[string]initStrings{
	"en": {
		LangPrompt: "Select language:",

		ConfigExists:    "Config already exists:",
		OverwritePrompt: "Overwrite? [y/N]:",
		Aborted:         "Aborted.",

		OpenClawDetected:     "OpenClaw installation detected at",
		OpenClawImport:       "Import basic config from OpenClaw? [Y/n]:",
		OpenClawImported:     "Imported %d config fields",
		OpenClawMigrateNote1: "For full workspace migration (roles, memory, skills, etc.),",
		OpenClawMigrateNote2: "run after setup: tetora import openclaw",

		Title: "=== Tetora Quick Setup ===",

		Step1Title: "Step 1/4: Choose a messaging channel",
		ChannelOptions: [4]string{
			"Telegram",
			"Discord",
			"Slack",
			"None (HTTP API only)",
		},

		TelegramHint1:        "How to get these values:",
		TelegramHint2:        "  1. Message @BotFather on Telegram → /newbot",
		TelegramHint3:        "  2. Copy the bot token it gives you",
		TelegramHint4:        "  3. Send a message to your bot, then visit:\n       https://api.telegram.org/bot<TOKEN>/getUpdates\n       to find your chat ID",
		TelegramTokenPrompt:  "Telegram bot token",
		TelegramChatIDPrompt: "Telegram chat ID",

		DiscordHint1:         "How to get these values:",
		DiscordHint2:         "  1. Go to https://discord.com/developers/applications",
		DiscordHint3:         "  2. Create an application (or select existing)",
		DiscordHint4:         "  3. Application ID → General Information page (top)\n  4. Bot → Reset Token → copy (this is the bot token)\n  5. Bot → scroll down → enable MESSAGE CONTENT INTENT",
		DiscordHint5:         "  6. Invite bot to your server:",
		DiscordHint6:         "     (no server yet? Discord left sidebar → '+' → Create My Own)\n     OAuth2 → URL Generator → check 'bot' in SCOPES\n     → check permissions (Send Messages, Read Message History)\n     → copy Generated URL at bottom → open in browser → select server",
		DiscordHint7:         "  7. Get channel ID:\n     Discord app → Settings (gear icon near your username)\n     → App Settings → Advanced → toggle Developer Mode ON\n     → go back, right-click the target channel → Copy Channel ID",
		DiscordTokenPrompt:   "Discord bot token",
		DiscordAppIDPrompt:   "Discord application ID",
		DiscordChannelPrompt: "Discord channel ID",

		SlackHint1:               "How to get these values:",
		SlackHint2:               "  1. Go to https://api.slack.com/apps → Create New App",
		SlackHint3:               "  2. Bot token → OAuth & Permissions → Install to Workspace\n     → copy the xoxb-... token\n  3. Signing secret → Basic Information → App Credentials",
		SlackTokenPrompt:         "Slack bot token (xoxb-...)",
		SlackSigningSecretPrompt: "Slack signing secret",

		Step2Title: "Step 2/4: Choose an AI provider",
		ProviderOptions: [3]string{
			"Claude CLI (local claude binary)",
			"Claude API (direct API key)",
			"OpenAI-compatible API",
		},
		ClaudeCLIPathPrompt:  "Claude CLI path",
		DefaultModelPrompt:   "Default model",
		ClaudeAPIKeyPrompt:   "Claude API key",
		OpenAIEndpointPrompt: "API endpoint",
		OpenAIKeyPrompt:      "API key",

		ClaudeCLIHint1: "Recommended if you have a Claude.ai Pro subscription ($20/mo).",
		ClaudeCLIHint2: "  Requires Claude Code CLI installed on your machine.",
		ClaudeCLIHint3: "  Install: npm install -g @anthropic-ai/claude-code",
		ClaudeCLIHint4: "  Login:   claude auth login   (sign in with your Claude.ai account)",
		ClaudeCLIHint5: "  Note: monthly usage limits apply depending on your plan tier.",
		ClaudeAPIHint1: "Requires a Claude API account (pay-per-use, billed separately).",
		ClaudeAPIHint2: "  Get API key: console.anthropic.com → API Keys",
		ClaudeAPIHint3: "  Note: this is separate from your Claude.ai Pro subscription.",

		Step3Title: "Step 3/4: Agent directory access",
		Step3Note1: "Agents need file access permissions (passed as --add-dir to Claude CLI).",
		Step3Note2: "The tetora data directory (~/.tetora/) is always included.",
		DirOptions: [3]string{
			"Home directory (~/)",
			"Specific directories (configure later in config.json)",
			"Tetora data only (~/.tetora/)",
		},
		DirInputPrompt: "Directories (comma-separated)",

		Step4Title: "Step 4/4: Generating config...",

		CreateRoleOCPrompt:    "OpenClaw agents can be imported later with: tetora import openclaw\n  Create a new agent now instead? [y/N]:",
		CreateRolePrompt:      "Create a first agent? [Y/n]:",
		RoleNamePrompt:        "Agent name",
		ArchetypeTitle:        "Start from a template?",
		ArchetypeBlank:        "Start from scratch",
		ArchetypeChoosePrompt: "Choose [1-%d]",
		SoulFilePrompt:        "Soul file path (empty for template)",
		RoleModelPrompt:       "Agent model",
		RoleDescPrompt:        "Description",
		RolePermPrompt:        "Permission mode (plan|acceptEdits|autoEdit|bypassPermissions)",
		RolePermInvalid:       "Unknown permission mode %q, using acceptEdits",
		RoleAdded:             "Agent %q added.",
		RoleError:             "Error saving agent: %v",

		SetDefaultAgentPrompt:   "Set %q as default agent? [Y/n]:",
		DefaultAgentSet:         "Default agent set to %q.",
		AutoRouteDiscordPrompt: "Auto-route Discord channels to %q? [Y/n]:",
		AutoRouteDiscordDone:   "Discord channels routed to %q.",
		AddAnotherRolePrompt:   "Add another agent? [y/N]:",
		EnableSmartDispatch:    "Enable smart dispatch (auto-route messages to best agent)? [Y/n]:",
		SmartDispatchEnabled:   "Smart dispatch enabled.",

		ServiceInstallPrompt: "Install as launchd service? [y/N]:",

		FinalConfig:   "Config:",
		FinalJobs:     "Jobs:",
		NextSteps:     "Next steps:",
		NextDoctor:    "  tetora doctor      Verify setup",
		NextStatus:    "  tetora status      Quick overview",
		NextServe:     "  tetora serve       Start daemon",
		NextDashboard: "  tetora dashboard   Open web UI",

		APITokenLabel: "API token:",
		APITokenNote:  "(Save this token — needed for CLI/API access)",
	},

	"zh-TW": {
		LangPrompt: "選擇語言:",

		ConfigExists:    "設定檔已存在:",
		OverwritePrompt: "要覆蓋嗎？[y/N]:",
		Aborted:         "已取消。",

		OpenClawDetected:     "偵測到 OpenClaw 安裝位置：",
		OpenClawImport:       "從 OpenClaw 匯入基本設定？[Y/n]:",
		OpenClawImported:     "已匯入 %d 個設定欄位",
		OpenClawMigrateNote1: "若需完整工作區遷移（角色、記憶、技能等），",
		OpenClawMigrateNote2: "請在設定完成後執行：tetora import openclaw",

		Title: "=== Tetora 快速設定 ===",

		Step1Title: "步驟 1/4：選擇訊息頻道",
		ChannelOptions: [4]string{
			"Telegram",
			"Discord",
			"Slack",
			"略過（僅 HTTP API）",
		},

		TelegramHint1:        "如何取得這些資訊：",
		TelegramHint2:        "  1. 在 Telegram 傳訊息給 @BotFather → /newbot",
		TelegramHint3:        "  2. 複製 BotFather 給你的 bot token",
		TelegramHint4:        "  3. 傳訊息給你的 bot，然後開啟：\n       https://api.telegram.org/bot<TOKEN>/getUpdates\n       找到你的 chat ID",
		TelegramTokenPrompt:  "Telegram bot token",
		TelegramChatIDPrompt: "Telegram chat ID",

		DiscordHint1:         "如何取得這些資訊：",
		DiscordHint2:         "  1. 前往 https://discord.com/developers/applications",
		DiscordHint3:         "  2. 建立應用程式（或選擇現有的）",
		DiscordHint4:         "  3. Application ID → General Information 頁面（最上方）\n  4. Bot → Reset Token → 複製（這就是 bot token）\n  5. Bot → 往下捲動 → 啟用 MESSAGE CONTENT INTENT",
		DiscordHint5:         "  6. 將 bot 邀請到你的伺服器：",
		DiscordHint6:         "     （還沒有伺服器？點左側欄的 '+' → 建立我的伺服器，建一個自己用的私人伺服器就好）\n     OAuth2 → URL Generator → 在 SCOPES 勾選 'bot'\n     → 勾選權限（Send Messages、Read Message History）\n     → 複製底部的 Generated URL → 在瀏覽器開啟 → 選擇伺服器",
		DiscordHint7:         "  7. 取得 channel ID：\n     Discord → 設定（使用者名稱旁的齒輪圖示）\n     → 應用程式設定 → 進階 → 開啟開發者模式\n     → 返回，右鍵點擊目標頻道 → 複製頻道 ID",
		DiscordTokenPrompt:   "Discord bot token",
		DiscordAppIDPrompt:   "Discord application ID",
		DiscordChannelPrompt: "Discord channel ID",

		SlackHint1:               "如何取得這些資訊：",
		SlackHint2:               "  1. 前往 https://api.slack.com/apps → Create New App",
		SlackHint3:               "  2. Bot token → OAuth & Permissions → Install to Workspace\n     → 複製 xoxb-... token\n  3. Signing secret → Basic Information → App Credentials",
		SlackTokenPrompt:         "Slack bot token (xoxb-...)",
		SlackSigningSecretPrompt: "Slack signing secret",

		Step2Title: "步驟 2/4：選擇 AI 提供者",
		ProviderOptions: [3]string{
			"Claude CLI（本機 claude 執行檔）",
			"Claude API（直接使用 API key）",
			"OpenAI 相容 API",
		},
		ClaudeCLIPathPrompt:  "Claude CLI 路徑",
		DefaultModelPrompt:   "預設模型",
		ClaudeAPIKeyPrompt:   "Claude API key",
		OpenAIEndpointPrompt: "API endpoint",
		OpenAIKeyPrompt:      "API key",

		ClaudeCLIHint1: "適合 Claude.ai Pro 訂閱用戶（月費 $20）。",
		ClaudeCLIHint2: "  需要先在電腦上安裝 Claude Code CLI。",
		ClaudeCLIHint3: "  安裝：npm install -g @anthropic-ai/claude-code",
		ClaudeCLIHint4: "  登入：claude auth login   （使用你的 Claude.ai 帳號登入）",
		ClaudeCLIHint5: "  注意：每月使用量有限制，依訂閱方案而定。",
		ClaudeAPIHint1: "需要 Claude API 帳號（按用量計費，與 Claude.ai 訂閱分開）。",
		ClaudeAPIHint2: "  取得 API key：console.anthropic.com → API Keys",
		ClaudeAPIHint3: "  注意：此帳號與 Claude.ai Pro 訂閱是完全分開的。",

		Step3Title: "步驟 3/4：Agent 目錄存取權限",
		Step3Note1: "Agent 需要檔案存取權限（傳入 --add-dir 給 Claude CLI）。",
		Step3Note2: "Tetora 資料目錄（~/.tetora/）一律包含在內。",
		DirOptions: [3]string{
			"家目錄（~/）",
			"指定目錄（稍後在 config.json 設定）",
			"僅 Tetora 資料（~/.tetora/）",
		},
		DirInputPrompt: "目錄（逗號分隔）",

		Step4Title: "步驟 4/4：產生設定檔中...",

		CreateRoleOCPrompt:    "OpenClaw 代理可稍後執行：tetora import openclaw 匯入\n  現在要建立新代理嗎？[y/N]:",
		CreateRolePrompt:      "建立第一個代理？[Y/n]:",
		RoleNamePrompt:        "代理名稱",
		ArchetypeTitle:        "從範本開始？",
		ArchetypeBlank:        "從零開始",
		ArchetypeChoosePrompt: "選擇 [1-%d]",
		SoulFilePrompt:        "Soul 檔案路徑（空白使用範本）",
		RoleModelPrompt:       "代理模型",
		RoleDescPrompt:        "描述",
		RolePermPrompt:        "權限模式 (plan|acceptEdits|autoEdit|bypassPermissions)",
		RolePermInvalid:       "未知的權限模式 %q，改用 acceptEdits",
		RoleAdded:             "代理 %q 已新增。",
		RoleError:             "儲存代理時發生錯誤：%v",

		SetDefaultAgentPrompt:   "將 %q 設為預設代理？[Y/n]:",
		DefaultAgentSet:         "已將預設代理設為 %q。",
		AutoRouteDiscordPrompt: "自動將 Discord 頻道路由到 %q？[Y/n]:",
		AutoRouteDiscordDone:   "Discord 頻道已路由到 %q。",
		AddAnotherRolePrompt:   "新增另一個代理？[y/N]:",
		EnableSmartDispatch:    "啟用智慧分派（自動路由訊息到最佳代理）？[Y/n]:",
		SmartDispatchEnabled:   "智慧分派已啟用。",

		ServiceInstallPrompt: "安裝為 launchd 服務？[y/N]:",

		FinalConfig:   "設定檔:",
		FinalJobs:     "工作檔:",
		NextSteps:     "後續步驟：",
		NextDoctor:    "  tetora doctor      驗證設定",
		NextStatus:    "  tetora status      快速概覽",
		NextServe:     "  tetora serve       啟動守護程式",
		NextDashboard: "  tetora dashboard   開啟網頁介面",

		APITokenLabel: "API token:",
		APITokenNote:  "（請保存此 token — CLI/API 存取時需要用到）",
	},

	"ja": {
		LangPrompt: "言語を選択してください:",

		ConfigExists:    "設定ファイルが既に存在します:",
		OverwritePrompt: "上書きしますか？[y/N]:",
		Aborted:         "キャンセルしました。",

		OpenClawDetected:     "OpenClaw のインストールを検出しました:",
		OpenClawImport:       "OpenClaw から基本設定をインポートしますか？[Y/n]:",
		OpenClawImported:     "%d 個の設定フィールドをインポートしました",
		OpenClawMigrateNote1: "ワークスペース全体のマイグレーション（ロール、メモリ、スキルなど）は、",
		OpenClawMigrateNote2: "セットアップ後に実行してください: tetora import openclaw",

		Title: "=== Tetora クイックセットアップ ===",

		Step1Title: "ステップ 1/4: メッセージングチャンネルを選択",
		ChannelOptions: [4]string{
			"Telegram",
			"Discord",
			"Slack",
			"スキップ（HTTP API のみ）",
		},

		TelegramHint1:        "取得方法:",
		TelegramHint2:        "  1. Telegram で @BotFather にメッセージ → /newbot",
		TelegramHint3:        "  2. 発行された bot token をコピー",
		TelegramHint4:        "  3. bot にメッセージを送り、以下にアクセス:\n       https://api.telegram.org/bot<TOKEN>/getUpdates\n       chat ID を確認",
		TelegramTokenPrompt:  "Telegram bot token",
		TelegramChatIDPrompt: "Telegram chat ID",

		DiscordHint1:         "取得方法:",
		DiscordHint2:         "  1. https://discord.com/developers/applications にアクセス",
		DiscordHint3:         "  2. アプリケーションを作成（または既存のものを選択）",
		DiscordHint4:         "  3. Application ID → General Information ページ（上部）\n  4. Bot → Reset Token → コピー（これが bot token）\n  5. Bot → 下へスクロール → MESSAGE CONTENT INTENT を有効化",
		DiscordHint5:         "  6. サーバーに bot を招待:",
		DiscordHint6:         "     （サーバーがない場合は Discord 左サイドバーの '+' → 自分用のプライベートサーバーを作成）\n     OAuth2 → URL Generator → SCOPES で 'bot' にチェック\n     → 権限にチェック（Send Messages、Read Message History）\n     → 下部の Generated URL をコピー → ブラウザで開く → サーバーを選択",
		DiscordHint7:         "  7. チャンネル ID の取得:\n     Discord → 設定（ユーザー名横の歯車アイコン）\n     → アプリ設定 → 詳細設定 → 開発者モードをオン\n     → 戻って対象チャンネルを右クリック → チャンネル ID をコピー",
		DiscordTokenPrompt:   "Discord bot token",
		DiscordAppIDPrompt:   "Discord application ID",
		DiscordChannelPrompt: "Discord channel ID",

		SlackHint1:               "取得方法:",
		SlackHint2:               "  1. https://api.slack.com/apps → Create New App",
		SlackHint3:               "  2. Bot token → OAuth & Permissions → Install to Workspace\n     → xoxb-... token をコピー\n  3. Signing secret → Basic Information → App Credentials",
		SlackTokenPrompt:         "Slack bot token (xoxb-...)",
		SlackSigningSecretPrompt: "Slack signing secret",

		Step2Title: "ステップ 2/4: AI プロバイダーを選択",
		ProviderOptions: [3]string{
			"Claude CLI（ローカルの claude バイナリ）",
			"Claude API（API key で直接接続）",
			"OpenAI 互換 API",
		},
		ClaudeCLIPathPrompt:  "Claude CLI のパス",
		DefaultModelPrompt:   "デフォルトモデル",
		ClaudeAPIKeyPrompt:   "Claude API key",
		OpenAIEndpointPrompt: "API endpoint",
		OpenAIKeyPrompt:      "API key",

		ClaudeCLIHint1: "Claude.ai Pro サブスクリプション（月額 $20）をお持ちの方に推奨。",
		ClaudeCLIHint2: "  Claude Code CLI のインストールが事前に必要です。",
		ClaudeCLIHint3: "  インストール: npm install -g @anthropic-ai/claude-code",
		ClaudeCLIHint4: "  ログイン: claude auth login   （Claude.ai アカウントでサインイン）",
		ClaudeCLIHint5: "  注意: プランによって月次使用量制限があります。",
		ClaudeAPIHint1: "Claude API アカウントが必要です（従量課金制、別途請求）。",
		ClaudeAPIHint2: "  API キー取得: console.anthropic.com → API Keys",
		ClaudeAPIHint3: "  注意: Claude.ai サブスクリプションとは別のアカウントです。",

		Step3Title: "ステップ 3/4: エージェントのディレクトリアクセス",
		Step3Note1: "エージェントにはファイルアクセス権限が必要です（Claude CLI に --add-dir として渡されます）。",
		Step3Note2: "Tetora データディレクトリ（~/.tetora/）は常に含まれます。",
		DirOptions: [3]string{
			"ホームディレクトリ（~/）",
			"指定ディレクトリ（後で config.json で設定）",
			"Tetora データのみ（~/.tetora/）",
		},
		DirInputPrompt: "ディレクトリ（カンマ区切り）",

		Step4Title: "ステップ 4/4: 設定ファイルを生成中...",

		CreateRoleOCPrompt:    "OpenClaw のエージェントは後で tetora import openclaw でインポートできます\n  今すぐ新しいエージェントを作成しますか？[y/N]:",
		CreateRolePrompt:      "最初のエージェントを作成しますか？[Y/n]:",
		RoleNamePrompt:        "エージェント名",
		ArchetypeTitle:        "テンプレートから始めますか？",
		ArchetypeBlank:        "ゼロから始める",
		ArchetypeChoosePrompt: "選択 [1-%d]",
		SoulFilePrompt:        "Soul ファイルのパス（空欄でテンプレート使用）",
		RoleModelPrompt:       "エージェントのモデル",
		RoleDescPrompt:        "説明",
		RolePermPrompt:        "権限モード (plan|acceptEdits|autoEdit|bypassPermissions)",
		RolePermInvalid:       "不明な権限モード %q、acceptEdits を使用します",
		RoleAdded:             "エージェント %q を追加しました。",
		RoleError:             "エージェントの保存中にエラー: %v",

		SetDefaultAgentPrompt:   "%q をデフォルトエージェントに設定しますか？[Y/n]:",
		DefaultAgentSet:         "デフォルトエージェントを %q に設定しました。",
		AutoRouteDiscordPrompt: "Discord チャンネルを %q に自動ルーティングしますか？[Y/n]:",
		AutoRouteDiscordDone:   "Discord チャンネルを %q にルーティングしました。",
		AddAnotherRolePrompt:   "別のエージェントを追加しますか？[y/N]:",
		EnableSmartDispatch:    "スマートディスパッチを有効にしますか（メッセージを最適なエージェントに自動ルーティング）？[Y/n]:",
		SmartDispatchEnabled:   "スマートディスパッチを有効にしました。",

		ServiceInstallPrompt: "launchd サービスとしてインストールしますか？[y/N]:",

		FinalConfig:   "設定ファイル:",
		FinalJobs:     "ジョブファイル:",
		NextSteps:     "次のステップ:",
		NextDoctor:    "  tetora doctor      セットアップを確認",
		NextStatus:    "  tetora status      クイック概要",
		NextServe:     "  tetora serve       デーモンを起動",
		NextDashboard: "  tetora dashboard   Web UI を開く",

		APITokenLabel: "API token:",
		APITokenNote:  "（このトークンを保存してください — CLI/API アクセスに必要です）",
	},

	"ko": {
		LangPrompt: "언어를 선택하세요:",

		ConfigExists:    "설정 파일이 이미 존재합니다:",
		OverwritePrompt: "덮어쓰시겠습니까? [y/N]:",
		Aborted:         "취소되었습니다.",

		OpenClawDetected:     "OpenClaw 설치가 감지되었습니다:",
		OpenClawImport:       "OpenClaw에서 기본 설정을 가져오시겠습니까? [Y/n]:",
		OpenClawImported:     "%d 개의 설정 필드를 가져왔습니다",
		OpenClawMigrateNote1: "전체 워크스페이스 마이그레이션(역할, 메모리, 스킬 등)은",
		OpenClawMigrateNote2: "설정 완료 후 실행하세요: tetora import openclaw",

		Title: "=== Tetora 빠른 설정 ===",

		Step1Title: "1/4단계: 메시징 채널 선택",
		ChannelOptions: [4]string{
			"Telegram",
			"Discord",
			"Slack",
			"건너뛰기 (HTTP API만 사용)",
		},

		TelegramHint1:        "값을 얻는 방법:",
		TelegramHint2:        "  1. Telegram에서 @BotFather에 메시지 → /newbot",
		TelegramHint3:        "  2. 발급된 bot token 복사",
		TelegramHint4:        "  3. bot에 메시지를 보낸 후 다음 주소 접속:\n       https://api.telegram.org/bot<TOKEN>/getUpdates\n       chat ID 확인",
		TelegramTokenPrompt:  "Telegram bot token",
		TelegramChatIDPrompt: "Telegram chat ID",

		DiscordHint1:         "값을 얻는 방법:",
		DiscordHint2:         "  1. https://discord.com/developers/applications 접속",
		DiscordHint3:         "  2. 애플리케이션 생성 (또는 기존 선택)",
		DiscordHint4:         "  3. Application ID → General Information 페이지 (상단)\n  4. Bot → Reset Token → 복사 (이것이 bot token)\n  5. Bot → 아래로 스크롤 → MESSAGE CONTENT INTENT 활성화",
		DiscordHint5:         "  6. 서버에 bot 초대:",
		DiscordHint6:         "     (서버가 없다면? Discord 왼쪽 사이드바 '+' → 내 서버 만들기)\n     OAuth2 → URL Generator → SCOPES에서 'bot' 체크\n     → 권한 체크 (Send Messages, Read Message History)\n     → 하단의 Generated URL 복사 → 브라우저에서 열기 → 서버 선택",
		DiscordHint7:         "  7. 채널 ID 가져오기:\n     Discord → 설정 (사용자 이름 옆 톱니바퀴 아이콘)\n     → 앱 설정 → 고급 → 개발자 모드 켜기\n     → 돌아가서 대상 채널 우클릭 → 채널 ID 복사",
		DiscordTokenPrompt:   "Discord bot token",
		DiscordAppIDPrompt:   "Discord application ID",
		DiscordChannelPrompt: "Discord channel ID",

		SlackHint1:               "값을 얻는 방법:",
		SlackHint2:               "  1. https://api.slack.com/apps → Create New App",
		SlackHint3:               "  2. Bot token → OAuth & Permissions → Install to Workspace\n     → xoxb-... token 복사\n  3. Signing secret → Basic Information → App Credentials",
		SlackTokenPrompt:         "Slack bot token (xoxb-...)",
		SlackSigningSecretPrompt: "Slack signing secret",

		Step2Title: "2/4단계: AI 제공자 선택",
		ProviderOptions: [3]string{
			"Claude CLI (로컬 claude 바이너리)",
			"Claude API (직접 API key 사용)",
			"OpenAI 호환 API",
		},
		ClaudeCLIPathPrompt:  "Claude CLI 경로",
		DefaultModelPrompt:   "기본 모델",
		ClaudeAPIKeyPrompt:   "Claude API key",
		OpenAIEndpointPrompt: "API endpoint",
		OpenAIKeyPrompt:      "API key",

		ClaudeCLIHint1: "Claude.ai Pro 구독자($20/월)에게 권장합니다.",
		ClaudeCLIHint2: "  Claude Code CLI가 먼저 설치되어 있어야 합니다.",
		ClaudeCLIHint3: "  설치: npm install -g @anthropic-ai/claude-code",
		ClaudeCLIHint4: "  로그인: claude auth login   (Claude.ai 계정으로 로그인)",
		ClaudeCLIHint5: "  참고: 플랜에 따라 월별 사용량 제한이 있습니다.",
		ClaudeAPIHint1: "Claude API 계정이 필요합니다 (사용량 기반 과금, 별도 청구).",
		ClaudeAPIHint2: "  API 키 발급: console.anthropic.com → API Keys",
		ClaudeAPIHint3: "  참고: Claude.ai 구독과는 완전히 별개의 계정입니다.",

		Step3Title: "3/4단계: 에이전트 디렉토리 접근",
		Step3Note1: "에이전트에는 파일 접근 권한이 필요합니다 (Claude CLI에 --add-dir로 전달됩니다).",
		Step3Note2: "Tetora 데이터 디렉토리 (~/.tetora/)는 항상 포함됩니다.",
		DirOptions: [3]string{
			"홈 디렉토리 (~/)",
			"특정 디렉토리 (나중에 config.json에서 설정)",
			"Tetora 데이터만 (~/.tetora/)",
		},
		DirInputPrompt: "디렉토리 (쉼표로 구분)",

		Step4Title: "4/4단계: 설정 생성 중...",

		CreateRoleOCPrompt:    "OpenClaw 에이전트는 나중에 tetora import openclaw로 가져올 수 있습니다\n  지금 새 에이전트를 만드시겠습니까? [y/N]:",
		CreateRolePrompt:      "첫 번째 에이전트를 만드시겠습니까? [Y/n]:",
		RoleNamePrompt:        "에이전트 이름",
		ArchetypeTitle:        "템플릿으로 시작하시겠습니까?",
		ArchetypeBlank:        "처음부터 시작",
		ArchetypeChoosePrompt: "선택 [1-%d]",
		SoulFilePrompt:        "Soul 파일 경로 (비워두면 템플릿 사용)",
		RoleModelPrompt:       "에이전트 모델",
		RoleDescPrompt:        "설명",
		RolePermPrompt:        "권한 모드 (plan|acceptEdits|autoEdit|bypassPermissions)",
		RolePermInvalid:       "알 수 없는 권한 모드 %q, acceptEdits를 사용합니다",
		RoleAdded:             "에이전트 %q이(가) 추가되었습니다.",
		RoleError:             "에이전트 저장 중 오류: %v",

		SetDefaultAgentPrompt:   "%q을(를) 기본 에이전트로 설정하시겠습니까? [Y/n]:",
		DefaultAgentSet:         "기본 에이전트가 %q(으)로 설정되었습니다.",
		AutoRouteDiscordPrompt: "Discord 채널을 %q(으)로 자동 라우팅하시겠습니까? [Y/n]:",
		AutoRouteDiscordDone:   "Discord 채널이 %q(으)로 라우팅되었습니다.",
		AddAnotherRolePrompt:   "다른 에이전트를 추가하시겠습니까? [y/N]:",
		EnableSmartDispatch:    "스마트 디스패치를 활성화하시겠습니까 (메시지를 최적 에이전트로 자동 라우팅)? [Y/n]:",
		SmartDispatchEnabled:   "스마트 디스패치가 활성화되었습니다.",

		ServiceInstallPrompt: "launchd 서비스로 설치하시겠습니까? [y/N]:",

		FinalConfig:   "설정 파일:",
		FinalJobs:     "작업 파일:",
		NextSteps:     "다음 단계:",
		NextDoctor:    "  tetora doctor      설정 확인",
		NextStatus:    "  tetora status      빠른 개요",
		NextServe:     "  tetora serve       데몬 시작",
		NextDashboard: "  tetora dashboard   웹 UI 열기",

		APITokenLabel: "API token:",
		APITokenNote:  "(이 토큰을 저장하세요 — CLI/API 접근에 필요합니다)",
	},

	"de": {
		LangPrompt: "Sprache auswählen:",

		ConfigExists:    "Konfiguration existiert bereits:",
		OverwritePrompt: "Überschreiben? [y/N]:",
		Aborted:         "Abgebrochen.",

		OpenClawDetected:     "OpenClaw-Installation gefunden unter",
		OpenClawImport:       "Basiskonfiguration von OpenClaw importieren? [Y/n]:",
		OpenClawImported:     "%d Konfigurationsfelder importiert",
		OpenClawMigrateNote1: "Für die vollständige Workspace-Migration (Rollen, Speicher, Skills usw.),",
		OpenClawMigrateNote2: "nach dem Setup ausführen: tetora import openclaw",

		Title: "=== Tetora Schnelleinrichtung ===",

		Step1Title: "Schritt 1/4: Messaging-Kanal wählen",
		ChannelOptions: [4]string{
			"Telegram",
			"Discord",
			"Slack",
			"Überspringen (nur HTTP API)",
		},

		TelegramHint1:        "So erhalten Sie diese Werte:",
		TelegramHint2:        "  1. Senden Sie eine Nachricht an @BotFather auf Telegram → /newbot",
		TelegramHint3:        "  2. Kopieren Sie den bot token",
		TelegramHint4:        "  3. Senden Sie eine Nachricht an Ihren Bot und besuchen Sie:\n       https://api.telegram.org/bot<TOKEN>/getUpdates\n       um Ihre chat ID zu finden",
		TelegramTokenPrompt:  "Telegram bot token",
		TelegramChatIDPrompt: "Telegram chat ID",

		DiscordHint1:         "So erhalten Sie diese Werte:",
		DiscordHint2:         "  1. Gehen Sie zu https://discord.com/developers/applications",
		DiscordHint3:         "  2. Erstellen Sie eine Anwendung (oder wählen Sie eine vorhandene)",
		DiscordHint4:         "  3. Application ID → Seite Allgemeine Informationen (oben)\n  4. Bot → Token zurücksetzen → kopieren (das ist der bot token)\n  5. Bot → nach unten scrollen → MESSAGE CONTENT INTENT aktivieren",
		DiscordHint5:         "  6. Bot zum Server einladen:",
		DiscordHint6:         "     (Noch kein Server? Discord linke Seitenleiste → '+' → Meinen eigenen erstellen)\n     OAuth2 → URL Generator → 'bot' in SCOPES anhaken\n     → Berechtigungen anhaken (Send Messages, Read Message History)\n     → Generierte URL unten kopieren → im Browser öffnen → Server auswählen",
		DiscordHint7:         "  7. Kanal-ID abrufen:\n     Discord → Einstellungen (Zahnrad-Symbol neben Ihrem Benutzernamen)\n     → App-Einstellungen → Erweitert → Entwicklermodus einschalten\n     → zurückgehen, Zielkanal rechtsklicken → Kanal-ID kopieren",
		DiscordTokenPrompt:   "Discord bot token",
		DiscordAppIDPrompt:   "Discord application ID",
		DiscordChannelPrompt: "Discord channel ID",

		SlackHint1:               "So erhalten Sie diese Werte:",
		SlackHint2:               "  1. Gehen Sie zu https://api.slack.com/apps → Create New App",
		SlackHint3:               "  2. Bot token → OAuth & Permissions → In Workspace installieren\n     → den xoxb-... token kopieren\n  3. Signing secret → Grundlegende Informationen → App-Anmeldedaten",
		SlackTokenPrompt:         "Slack bot token (xoxb-...)",
		SlackSigningSecretPrompt: "Slack signing secret",

		Step2Title: "Schritt 2/4: KI-Anbieter wählen",
		ProviderOptions: [3]string{
			"Claude CLI (lokale claude-Binärdatei)",
			"Claude API (direkter API key)",
			"OpenAI-kompatible API",
		},
		ClaudeCLIPathPrompt:  "Claude CLI-Pfad",
		DefaultModelPrompt:   "Standardmodell",
		ClaudeAPIKeyPrompt:   "Claude API key",
		OpenAIEndpointPrompt: "API endpoint",
		OpenAIKeyPrompt:      "API key",

		ClaudeCLIHint1: "Empfohlen für Claude.ai Pro-Abonnenten ($20/Monat).",
		ClaudeCLIHint2: "  Claude Code CLI muss zuerst installiert werden.",
		ClaudeCLIHint3: "  Installation: npm install -g @anthropic-ai/claude-code",
		ClaudeCLIHint4: "  Anmeldung: claude auth login   (mit Claude.ai-Konto anmelden)",
		ClaudeCLIHint5: "  Hinweis: Monatliche Nutzungslimits gelten je nach Plan.",
		ClaudeAPIHint1: "Erfordert ein Claude API-Konto (nutzungsbasierte Abrechnung, separat).",
		ClaudeAPIHint2: "  API-Schlüssel: console.anthropic.com → API Keys",
		ClaudeAPIHint3: "  Hinweis: Getrennt vom Claude.ai Pro-Abonnement.",

		Step3Title: "Schritt 3/4: Verzeichniszugriff für Agenten",
		Step3Note1: "Agenten benötigen Dateizugriffsberechtigungen (als --add-dir an Claude CLI übergeben).",
		Step3Note2: "Das Tetora-Datenverzeichnis (~/.tetora/) ist immer enthalten.",
		DirOptions: [3]string{
			"Home-Verzeichnis (~/)",
			"Bestimmte Verzeichnisse (später in config.json konfigurieren)",
			"Nur Tetora-Daten (~/.tetora/)",
		},
		DirInputPrompt: "Verzeichnisse (kommagetrennt)",

		Step4Title: "Schritt 4/4: Konfiguration wird erstellt...",

		CreateRoleOCPrompt:    "OpenClaw-Agenten können später importiert werden: tetora import openclaw\n  Jetzt einen neuen Agenten erstellen? [y/N]:",
		CreateRolePrompt:      "Ersten Agenten erstellen? [Y/n]:",
		RoleNamePrompt:        "Agentenname",
		ArchetypeTitle:        "Von einer Vorlage starten?",
		ArchetypeBlank:        "Von vorne beginnen",
		ArchetypeChoosePrompt: "Auswahl [1-%d]",
		SoulFilePrompt:        "Soul-Dateipfad (leer für Vorlage)",
		RoleModelPrompt:       "Agentenmodell",
		RoleDescPrompt:        "Beschreibung",
		RolePermPrompt:        "Berechtigungsmodus (plan|acceptEdits|autoEdit|bypassPermissions)",
		RolePermInvalid:       "Unbekannter Berechtigungsmodus %q, verwende acceptEdits",
		RoleAdded:             "Agent %q hinzugefügt.",
		RoleError:             "Fehler beim Speichern des Agenten: %v",

		SetDefaultAgentPrompt:   "%q als Standard-Agent festlegen? [Y/n]:",
		DefaultAgentSet:         "Standard-Agent auf %q gesetzt.",
		AutoRouteDiscordPrompt: "Discord-Kanäle automatisch zu %q routen? [Y/n]:",
		AutoRouteDiscordDone:   "Discord-Kanäle zu %q geroutet.",
		AddAnotherRolePrompt:   "Weiteren Agenten hinzufügen? [y/N]:",
		EnableSmartDispatch:    "Smart Dispatch aktivieren (Nachrichten automatisch an besten Agent routen)? [Y/n]:",
		SmartDispatchEnabled:   "Smart Dispatch aktiviert.",

		ServiceInstallPrompt: "Als launchd-Dienst installieren? [y/N]:",

		FinalConfig:   "Konfiguration:",
		FinalJobs:     "Jobs:",
		NextSteps:     "Nächste Schritte:",
		NextDoctor:    "  tetora doctor      Setup überprüfen",
		NextStatus:    "  tetora status      Schnellübersicht",
		NextServe:     "  tetora serve       Daemon starten",
		NextDashboard: "  tetora dashboard   Web-UI öffnen",

		APITokenLabel: "API token:",
		APITokenNote:  "(Diesen Token speichern — für CLI/API-Zugriff benötigt)",
	},

	"es": {
		LangPrompt: "Seleccionar idioma:",

		ConfigExists:    "La configuración ya existe:",
		OverwritePrompt: "¿Sobrescribir? [y/N]:",
		Aborted:         "Cancelado.",

		OpenClawDetected:     "Instalación de OpenClaw detectada en",
		OpenClawImport:       "¿Importar configuración básica de OpenClaw? [Y/n]:",
		OpenClawImported:     "Se importaron %d campos de configuración",
		OpenClawMigrateNote1: "Para la migración completa del espacio de trabajo (roles, memoria, habilidades, etc.),",
		OpenClawMigrateNote2: "ejecute después de la configuración: tetora import openclaw",

		Title: "=== Configuración rápida de Tetora ===",

		Step1Title: "Paso 1/4: Elegir canal de mensajería",
		ChannelOptions: [4]string{
			"Telegram",
			"Discord",
			"Slack",
			"Omitir (solo HTTP API)",
		},

		TelegramHint1:        "Cómo obtener estos valores:",
		TelegramHint2:        "  1. Envíe un mensaje a @BotFather en Telegram → /newbot",
		TelegramHint3:        "  2. Copie el bot token que le proporciona",
		TelegramHint4:        "  3. Envíe un mensaje a su bot y visite:\n       https://api.telegram.org/bot<TOKEN>/getUpdates\n       para encontrar su chat ID",
		TelegramTokenPrompt:  "Telegram bot token",
		TelegramChatIDPrompt: "Telegram chat ID",

		DiscordHint1:         "Cómo obtener estos valores:",
		DiscordHint2:         "  1. Vaya a https://discord.com/developers/applications",
		DiscordHint3:         "  2. Cree una aplicación (o seleccione una existente)",
		DiscordHint4:         "  3. Application ID → página General Information (arriba)\n  4. Bot → Reset Token → copiar (este es el bot token)\n  5. Bot → desplácese hacia abajo → habilite MESSAGE CONTENT INTENT",
		DiscordHint5:         "  6. Invite al bot a su servidor:",
		DiscordHint6:         "     (¿Sin servidor? Barra lateral izquierda de Discord → '+' → Crear el mío)\n     OAuth2 → URL Generator → marque 'bot' en SCOPES\n     → marque permisos (Send Messages, Read Message History)\n     → copie la URL generada al final → ábrala en el navegador → seleccione el servidor",
		DiscordHint7:         "  7. Obtener el channel ID:\n     Discord → Configuración (ícono de engranaje cerca de su nombre)\n     → Configuración de la aplicación → Avanzado → activar Modo desarrollador\n     → vuelva, haga clic derecho en el canal objetivo → Copiar ID del canal",
		DiscordTokenPrompt:   "Discord bot token",
		DiscordAppIDPrompt:   "Discord application ID",
		DiscordChannelPrompt: "Discord channel ID",

		SlackHint1:               "Cómo obtener estos valores:",
		SlackHint2:               "  1. Vaya a https://api.slack.com/apps → Create New App",
		SlackHint3:               "  2. Bot token → OAuth & Permissions → Install to Workspace\n     → copie el token xoxb-...\n  3. Signing secret → Basic Information → App Credentials",
		SlackTokenPrompt:         "Slack bot token (xoxb-...)",
		SlackSigningSecretPrompt: "Slack signing secret",

		Step2Title: "Paso 2/4: Elegir proveedor de IA",
		ProviderOptions: [3]string{
			"Claude CLI (binario claude local)",
			"Claude API (API key directa)",
			"API compatible con OpenAI",
		},
		ClaudeCLIPathPrompt:  "Ruta de Claude CLI",
		DefaultModelPrompt:   "Modelo predeterminado",
		ClaudeAPIKeyPrompt:   "Claude API key",
		OpenAIEndpointPrompt: "API endpoint",
		OpenAIKeyPrompt:      "API key",

		ClaudeCLIHint1: "Recomendado para suscriptores de Claude.ai Pro ($20/mes).",
		ClaudeCLIHint2: "  Requiere Claude Code CLI instalado en tu equipo.",
		ClaudeCLIHint3: "  Instalar: npm install -g @anthropic-ai/claude-code",
		ClaudeCLIHint4: "  Iniciar sesión: claude auth login   (con tu cuenta de Claude.ai)",
		ClaudeCLIHint5: "  Nota: se aplican límites de uso mensual según el plan.",
		ClaudeAPIHint1: "Requiere una cuenta de Claude API (pago por uso, facturación separada).",
		ClaudeAPIHint2: "  Obtener API key: console.anthropic.com → API Keys",
		ClaudeAPIHint3: "  Nota: separada de tu suscripción de Claude.ai Pro.",

		Step3Title: "Paso 3/4: Acceso a directorios del agente",
		Step3Note1: "Los agentes necesitan permisos de acceso a archivos (se pasan como --add-dir a Claude CLI).",
		Step3Note2: "El directorio de datos de Tetora (~/.tetora/) siempre está incluido.",
		DirOptions: [3]string{
			"Directorio principal (~/)",
			"Directorios específicos (configurar más tarde en config.json)",
			"Solo datos de Tetora (~/.tetora/)",
		},
		DirInputPrompt: "Directorios (separados por comas)",

		Step4Title: "Paso 4/4: Generando configuración...",

		CreateRoleOCPrompt:    "Los agentes de OpenClaw se pueden importar más tarde con: tetora import openclaw\n  ¿Crear un nuevo agente ahora? [y/N]:",
		CreateRolePrompt:      "¿Crear un primer agente? [Y/n]:",
		RoleNamePrompt:        "Nombre del agente",
		ArchetypeTitle:        "¿Empezar desde una plantilla?",
		ArchetypeBlank:        "Comenzar desde cero",
		ArchetypeChoosePrompt: "Elegir [1-%d]",
		SoulFilePrompt:        "Ruta del archivo Soul (vacío para plantilla)",
		RoleModelPrompt:       "Modelo del agente",
		RoleDescPrompt:        "Descripción",
		RolePermPrompt:        "Modo de permiso (plan|acceptEdits|autoEdit|bypassPermissions)",
		RolePermInvalid:       "Modo de permiso desconocido %q, usando acceptEdits",
		RoleAdded:             "Agente %q añadido.",
		RoleError:             "Error al guardar el agente: %v",

		SetDefaultAgentPrompt:   "¿Establecer %q como agente predeterminado? [Y/n]:",
		DefaultAgentSet:         "Agente predeterminado establecido en %q.",
		AutoRouteDiscordPrompt: "¿Enrutar automáticamente canales de Discord a %q? [Y/n]:",
		AutoRouteDiscordDone:   "Canales de Discord enrutados a %q.",
		AddAnotherRolePrompt:   "¿Agregar otro agente? [y/N]:",
		EnableSmartDispatch:    "¿Habilitar despacho inteligente (enrutar mensajes automáticamente al mejor agente)? [Y/n]:",
		SmartDispatchEnabled:   "Despacho inteligente habilitado.",

		ServiceInstallPrompt: "¿Instalar como servicio launchd? [y/N]:",

		FinalConfig:   "Configuración:",
		FinalJobs:     "Trabajos:",
		NextSteps:     "Próximos pasos:",
		NextDoctor:    "  tetora doctor      Verificar configuración",
		NextStatus:    "  tetora status      Vista rápida",
		NextServe:     "  tetora serve       Iniciar daemon",
		NextDashboard: "  tetora dashboard   Abrir interfaz web",

		APITokenLabel: "API token:",
		APITokenNote:  "(Guarde este token — necesario para acceso CLI/API)",
	},

	"fr": {
		LangPrompt: "Sélectionner la langue:",

		ConfigExists:    "La configuration existe déjà:",
		OverwritePrompt: "Écraser? [y/N]:",
		Aborted:         "Annulé.",

		OpenClawDetected:     "Installation OpenClaw détectée à",
		OpenClawImport:       "Importer la configuration de base depuis OpenClaw? [Y/n]:",
		OpenClawImported:     "%d champs de configuration importés",
		OpenClawMigrateNote1: "Pour la migration complète de l'espace de travail (rôles, mémoire, compétences, etc.),",
		OpenClawMigrateNote2: "exécutez après la configuration: tetora import openclaw",

		Title: "=== Configuration rapide de Tetora ===",

		Step1Title: "Étape 1/4: Choisir un canal de messagerie",
		ChannelOptions: [4]string{
			"Telegram",
			"Discord",
			"Slack",
			"Ignorer (HTTP API uniquement)",
		},

		TelegramHint1:        "Comment obtenir ces valeurs:",
		TelegramHint2:        "  1. Envoyez un message à @BotFather sur Telegram → /newbot",
		TelegramHint3:        "  2. Copiez le bot token qu'il vous donne",
		TelegramHint4:        "  3. Envoyez un message à votre bot, puis visitez:\n       https://api.telegram.org/bot<TOKEN>/getUpdates\n       pour trouver votre chat ID",
		TelegramTokenPrompt:  "Telegram bot token",
		TelegramChatIDPrompt: "Telegram chat ID",

		DiscordHint1:         "Comment obtenir ces valeurs:",
		DiscordHint2:         "  1. Allez sur https://discord.com/developers/applications",
		DiscordHint3:         "  2. Créez une application (ou sélectionnez-en une existante)",
		DiscordHint4:         "  3. Application ID → page General Information (en haut)\n  4. Bot → Reset Token → copier (c'est le bot token)\n  5. Bot → faites défiler vers le bas → activez MESSAGE CONTENT INTENT",
		DiscordHint5:         "  6. Invitez le bot sur votre serveur:",
		DiscordHint6:         "     (Pas de serveur? Barre latérale gauche Discord → '+' → Créer le mien)\n     OAuth2 → URL Generator → cochez 'bot' dans SCOPES\n     → cochez les permissions (Send Messages, Read Message History)\n     → copiez l'URL générée en bas → ouvrez dans le navigateur → sélectionnez le serveur",
		DiscordHint7:         "  7. Obtenir le channel ID:\n     Discord → Paramètres (icône d'engrenage près de votre nom)\n     → Paramètres de l'application → Avancé → activer le Mode développeur\n     → revenez, faites un clic droit sur le canal cible → Copier l'ID du canal",
		DiscordTokenPrompt:   "Discord bot token",
		DiscordAppIDPrompt:   "Discord application ID",
		DiscordChannelPrompt: "Discord channel ID",

		SlackHint1:               "Comment obtenir ces valeurs:",
		SlackHint2:               "  1. Allez sur https://api.slack.com/apps → Create New App",
		SlackHint3:               "  2. Bot token → OAuth & Permissions → Installer dans l'espace de travail\n     → copiez le token xoxb-...\n  3. Signing secret → Informations de base → Identifiants de l'application",
		SlackTokenPrompt:         "Slack bot token (xoxb-...)",
		SlackSigningSecretPrompt: "Slack signing secret",

		Step2Title: "Étape 2/4: Choisir un fournisseur d'IA",
		ProviderOptions: [3]string{
			"Claude CLI (binaire claude local)",
			"Claude API (clé API directe)",
			"API compatible OpenAI",
		},
		ClaudeCLIPathPrompt:  "Chemin de Claude CLI",
		DefaultModelPrompt:   "Modèle par défaut",
		ClaudeAPIKeyPrompt:   "Claude API key",
		OpenAIEndpointPrompt: "API endpoint",
		OpenAIKeyPrompt:      "API key",

		ClaudeCLIHint1: "Recommandé pour les abonnés Claude.ai Pro (20 $/mois).",
		ClaudeCLIHint2: "  Claude Code CLI doit être installé au préalable.",
		ClaudeCLIHint3: "  Installation : npm install -g @anthropic-ai/claude-code",
		ClaudeCLIHint4: "  Connexion : claude auth login   (avec votre compte Claude.ai)",
		ClaudeCLIHint5: "  Note : des limites d'utilisation mensuelles s'appliquent selon le plan.",
		ClaudeAPIHint1: "Nécessite un compte Claude API (facturation à l'usage, séparée).",
		ClaudeAPIHint2: "  Obtenir une clé API : console.anthropic.com → API Keys",
		ClaudeAPIHint3: "  Note : compte distinct de votre abonnement Claude.ai Pro.",

		Step3Title: "Étape 3/4: Accès aux répertoires de l'agent",
		Step3Note1: "Les agents ont besoin de permissions d'accès aux fichiers (passées comme --add-dir à Claude CLI).",
		Step3Note2: "Le répertoire de données Tetora (~/.tetora/) est toujours inclus.",
		DirOptions: [3]string{
			"Répertoire personnel (~/)",
			"Répertoires spécifiques (configurer plus tard dans config.json)",
			"Données Tetora uniquement (~/.tetora/)",
		},
		DirInputPrompt: "Répertoires (séparés par des virgules)",

		Step4Title: "Étape 4/4: Génération de la configuration...",

		CreateRoleOCPrompt:    "Les agents OpenClaw peuvent être importés plus tard avec: tetora import openclaw\n  Créer un nouvel agent maintenant? [y/N]:",
		CreateRolePrompt:      "Créer un premier agent? [Y/n]:",
		RoleNamePrompt:        "Nom de l'agent",
		ArchetypeTitle:        "Commencer à partir d'un modèle?",
		ArchetypeBlank:        "Commencer de zéro",
		ArchetypeChoosePrompt: "Choisir [1-%d]",
		SoulFilePrompt:        "Chemin du fichier Soul (vide pour le modèle)",
		RoleModelPrompt:       "Modèle de l'agent",
		RoleDescPrompt:        "Description",
		RolePermPrompt:        "Mode de permission (plan|acceptEdits|autoEdit|bypassPermissions)",
		RolePermInvalid:       "Mode de permission inconnu %q, utilisation de acceptEdits",
		RoleAdded:             "Agent %q ajouté.",
		RoleError:             "Erreur lors de la sauvegarde de l'agent: %v",

		SetDefaultAgentPrompt:   "Définir %q comme agent par défaut ? [Y/n] :",
		DefaultAgentSet:         "Agent par défaut défini sur %q.",
		AutoRouteDiscordPrompt: "Router automatiquement les canaux Discord vers %q ? [Y/n] :",
		AutoRouteDiscordDone:   "Canaux Discord routés vers %q.",
		AddAnotherRolePrompt:   "Ajouter un autre agent ? [y/N] :",
		EnableSmartDispatch:    "Activer le dispatch intelligent (router automatiquement les messages vers le meilleur agent) ? [Y/n] :",
		SmartDispatchEnabled:   "Dispatch intelligent activé.",

		ServiceInstallPrompt: "Installer comme service launchd? [y/N]:",

		FinalConfig:   "Configuration:",
		FinalJobs:     "Jobs:",
		NextSteps:     "Prochaines étapes:",
		NextDoctor:    "  tetora doctor      Vérifier la configuration",
		NextStatus:    "  tetora status      Aperçu rapide",
		NextServe:     "  tetora serve       Démarrer le daemon",
		NextDashboard: "  tetora dashboard   Ouvrir l'interface web",

		APITokenLabel: "API token:",
		APITokenNote:  "(Sauvegardez ce token — nécessaire pour l'accès CLI/API)",
	},

	"id": {
		LangPrompt: "Pilih bahasa:",

		ConfigExists:    "Konfigurasi sudah ada:",
		OverwritePrompt: "Timpa? [y/N]:",
		Aborted:         "Dibatalkan.",

		OpenClawDetected:     "Instalasi OpenClaw terdeteksi di",
		OpenClawImport:       "Impor konfigurasi dasar dari OpenClaw? [Y/n]:",
		OpenClawImported:     "Berhasil mengimpor %d bidang konfigurasi",
		OpenClawMigrateNote1: "Untuk migrasi workspace lengkap (peran, memori, keahlian, dll.),",
		OpenClawMigrateNote2: "jalankan setelah setup: tetora import openclaw",

		Title: "=== Pengaturan Cepat Tetora ===",

		Step1Title: "Langkah 1/4: Pilih saluran pesan",
		ChannelOptions: [4]string{
			"Telegram",
			"Discord",
			"Slack",
			"Lewati (hanya HTTP API)",
		},

		TelegramHint1:        "Cara mendapatkan nilai-nilai ini:",
		TelegramHint2:        "  1. Kirim pesan ke @BotFather di Telegram → /newbot",
		TelegramHint3:        "  2. Salin bot token yang diberikan",
		TelegramHint4:        "  3. Kirim pesan ke bot Anda, lalu kunjungi:\n       https://api.telegram.org/bot<TOKEN>/getUpdates\n       untuk menemukan chat ID Anda",
		TelegramTokenPrompt:  "Telegram bot token",
		TelegramChatIDPrompt: "Telegram chat ID",

		DiscordHint1:         "Cara mendapatkan nilai-nilai ini:",
		DiscordHint2:         "  1. Buka https://discord.com/developers/applications",
		DiscordHint3:         "  2. Buat aplikasi (atau pilih yang sudah ada)",
		DiscordHint4:         "  3. Application ID → halaman General Information (atas)\n  4. Bot → Reset Token → salin (ini adalah bot token)\n  5. Bot → gulir ke bawah → aktifkan MESSAGE CONTENT INTENT",
		DiscordHint5:         "  6. Undang bot ke server Anda:",
		DiscordHint6:         "     (Belum punya server? Bilah sisi kiri Discord → '+' → Buat Sendiri)\n     OAuth2 → URL Generator → centang 'bot' di SCOPES\n     → centang izin (Send Messages, Read Message History)\n     → salin Generated URL di bawah → buka di browser → pilih server",
		DiscordHint7:         "  7. Dapatkan channel ID:\n     Discord → Pengaturan (ikon roda gigi di dekat nama Anda)\n     → Pengaturan Aplikasi → Lanjutan → aktifkan Mode Pengembang\n     → kembali, klik kanan saluran target → Salin ID Saluran",
		DiscordTokenPrompt:   "Discord bot token",
		DiscordAppIDPrompt:   "Discord application ID",
		DiscordChannelPrompt: "Discord channel ID",

		SlackHint1:               "Cara mendapatkan nilai-nilai ini:",
		SlackHint2:               "  1. Buka https://api.slack.com/apps → Create New App",
		SlackHint3:               "  2. Bot token → OAuth & Permissions → Install to Workspace\n     → salin token xoxb-...\n  3. Signing secret → Basic Information → App Credentials",
		SlackTokenPrompt:         "Slack bot token (xoxb-...)",
		SlackSigningSecretPrompt: "Slack signing secret",

		Step2Title: "Langkah 2/4: Pilih penyedia AI",
		ProviderOptions: [3]string{
			"Claude CLI (binary claude lokal)",
			"Claude API (API key langsung)",
			"API kompatibel OpenAI",
		},
		ClaudeCLIPathPrompt:  "Jalur Claude CLI",
		DefaultModelPrompt:   "Model default",
		ClaudeAPIKeyPrompt:   "Claude API key",
		OpenAIEndpointPrompt: "API endpoint",
		OpenAIKeyPrompt:      "API key",

		ClaudeCLIHint1: "Direkomendasikan untuk pelanggan Claude.ai Pro ($20/bulan).",
		ClaudeCLIHint2: "  Memerlukan Claude Code CLI yang terinstal di komputer Anda.",
		ClaudeCLIHint3: "  Pasang: npm install -g @anthropic-ai/claude-code",
		ClaudeCLIHint4: "  Masuk: claude auth login   (gunakan akun Claude.ai Anda)",
		ClaudeCLIHint5: "  Catatan: ada batas penggunaan bulanan sesuai paket langganan.",
		ClaudeAPIHint1: "Memerlukan akun Claude API (bayar sesuai penggunaan, ditagih terpisah).",
		ClaudeAPIHint2: "  Dapatkan API key: console.anthropic.com → API Keys",
		ClaudeAPIHint3: "  Catatan: terpisah dari langganan Claude.ai Pro Anda.",

		Step3Title: "Langkah 3/4: Akses direktori agen",
		Step3Note1: "Agen memerlukan izin akses file (diteruskan sebagai --add-dir ke Claude CLI).",
		Step3Note2: "Direktori data Tetora (~/.tetora/) selalu disertakan.",
		DirOptions: [3]string{
			"Direktori home (~/)",
			"Direktori tertentu (konfigurasi nanti di config.json)",
			"Hanya data Tetora (~/.tetora/)",
		},
		DirInputPrompt: "Direktori (dipisahkan koma)",

		Step4Title: "Langkah 4/4: Membuat konfigurasi...",

		CreateRoleOCPrompt:    "Agen OpenClaw dapat diimpor nanti dengan: tetora import openclaw\n  Buat agen baru sekarang? [y/N]:",
		CreateRolePrompt:      "Buat agen pertama? [Y/n]:",
		RoleNamePrompt:        "Nama agen",
		ArchetypeTitle:        "Mulai dari template?",
		ArchetypeBlank:        "Mulai dari awal",
		ArchetypeChoosePrompt: "Pilih [1-%d]",
		SoulFilePrompt:        "Jalur file Soul (kosong untuk template)",
		RoleModelPrompt:       "Model agen",
		RoleDescPrompt:        "Deskripsi",
		RolePermPrompt:        "Mode izin (plan|acceptEdits|autoEdit|bypassPermissions)",
		RolePermInvalid:       "Mode izin tidak dikenal %q, menggunakan acceptEdits",
		RoleAdded:             "Agen %q ditambahkan.",
		RoleError:             "Kesalahan menyimpan agen: %v",

		SetDefaultAgentPrompt:   "Tetapkan %q sebagai agen default? [Y/n]:",
		DefaultAgentSet:         "Agen default diatur ke %q.",
		AutoRouteDiscordPrompt: "Otomatis arahkan channel Discord ke %q? [Y/n]:",
		AutoRouteDiscordDone:   "Channel Discord diarahkan ke %q.",
		AddAnotherRolePrompt:   "Tambah agen lain? [y/N]:",
		EnableSmartDispatch:    "Aktifkan dispatch cerdas (otomatis arahkan pesan ke agen terbaik)? [Y/n]:",
		SmartDispatchEnabled:   "Dispatch cerdas diaktifkan.",

		ServiceInstallPrompt: "Instal sebagai layanan launchd? [y/N]:",

		FinalConfig:   "Konfigurasi:",
		FinalJobs:     "Jobs:",
		NextSteps:     "Langkah selanjutnya:",
		NextDoctor:    "  tetora doctor      Verifikasi setup",
		NextStatus:    "  tetora status      Ikhtisar cepat",
		NextServe:     "  tetora serve       Mulai daemon",
		NextDashboard: "  tetora dashboard   Buka antarmuka web",

		APITokenLabel: "API token:",
		APITokenNote:  "(Simpan token ini — diperlukan untuk akses CLI/API)",
	},

	"fil": {
		LangPrompt: "Pumili ng wika:",

		ConfigExists:    "Mayroon nang configuration:",
		OverwritePrompt: "I-overwrite? [y/N]:",
		Aborted:         "Kinansela.",

		OpenClawDetected:     "Natuklasan ang OpenClaw sa",
		OpenClawImport:       "I-import ang basic config mula sa OpenClaw? [Y/n]:",
		OpenClawImported:     "Na-import ang %d config fields",
		OpenClawMigrateNote1: "Para sa kumpletong workspace migration (mga role, memory, skills, atbp.),",
		OpenClawMigrateNote2: "patakbuhin pagkatapos ng setup: tetora import openclaw",

		Title: "=== Mabilis na Setup ng Tetora ===",

		Step1Title: "Hakbang 1/4: Pumili ng messaging channel",
		ChannelOptions: [4]string{
			"Telegram",
			"Discord",
			"Slack",
			"Laktawan (HTTP API lamang)",
		},

		TelegramHint1:        "Paano makuha ang mga value na ito:",
		TelegramHint2:        "  1. Mag-message sa @BotFather sa Telegram → /newbot",
		TelegramHint3:        "  2. Kopyahin ang bot token na ibibigay nito",
		TelegramHint4:        "  3. Mag-send ng message sa iyong bot, pagkatapos bisitahin:\n       https://api.telegram.org/bot<TOKEN>/getUpdates\n       para makita ang iyong chat ID",
		TelegramTokenPrompt:  "Telegram bot token",
		TelegramChatIDPrompt: "Telegram chat ID",

		DiscordHint1:         "Paano makuha ang mga value na ito:",
		DiscordHint2:         "  1. Pumunta sa https://discord.com/developers/applications",
		DiscordHint3:         "  2. Gumawa ng application (o pumili ng existing)",
		DiscordHint4:         "  3. Application ID → pahina ng General Information (itaas)\n  4. Bot → Reset Token → kopyahin (ito ang bot token)\n  5. Bot → mag-scroll pababa → i-enable ang MESSAGE CONTENT INTENT",
		DiscordHint5:         "  6. I-invite ang bot sa iyong server:",
		DiscordHint6:         "     (Wala pang server? Discord left sidebar → '+' → Gumawa ng sarili)\n     OAuth2 → URL Generator → lagyan ng check ang 'bot' sa SCOPES\n     → lagyan ng check ang mga permission (Send Messages, Read Message History)\n     → kopyahin ang Generated URL sa ibaba → buksan sa browser → piliin ang server",
		DiscordHint7:         "  7. Kunin ang channel ID:\n     Discord → Settings (gear icon malapit sa iyong username)\n     → App Settings → Advanced → i-toggle ang Developer Mode\n     → bumalik, i-right-click ang target channel → Kopyahin ang Channel ID",
		DiscordTokenPrompt:   "Discord bot token",
		DiscordAppIDPrompt:   "Discord application ID",
		DiscordChannelPrompt: "Discord channel ID",

		SlackHint1:               "Paano makuha ang mga value na ito:",
		SlackHint2:               "  1. Pumunta sa https://api.slack.com/apps → Create New App",
		SlackHint3:               "  2. Bot token → OAuth & Permissions → I-install sa Workspace\n     → kopyahin ang xoxb-... token\n  3. Signing secret → Basic Information → App Credentials",
		SlackTokenPrompt:         "Slack bot token (xoxb-...)",
		SlackSigningSecretPrompt: "Slack signing secret",

		Step2Title: "Hakbang 2/4: Pumili ng AI provider",
		ProviderOptions: [3]string{
			"Claude CLI (lokal na claude binary)",
			"Claude API (direktang API key)",
			"OpenAI-compatible API",
		},
		ClaudeCLIPathPrompt:  "Claude CLI path",
		DefaultModelPrompt:   "Default na modelo",
		ClaudeAPIKeyPrompt:   "Claude API key",
		OpenAIEndpointPrompt: "API endpoint",
		OpenAIKeyPrompt:      "API key",

		ClaudeCLIHint1: "Inirerekomenda para sa mga subscriber ng Claude.ai Pro ($20/buwan).",
		ClaudeCLIHint2: "  Kailangan ang Claude Code CLI na naka-install sa inyong computer.",
		ClaudeCLIHint3: "  I-install: npm install -g @anthropic-ai/claude-code",
		ClaudeCLIHint4: "  Mag-login: claude auth login   (gamit ang inyong Claude.ai account)",
		ClaudeCLIHint5: "  Tandaan: may limitasyon sa buwanang paggamit ayon sa plano.",
		ClaudeAPIHint1: "Kailangan ang Claude API account (bayad batay sa paggamit, hiwalay na bayad).",
		ClaudeAPIHint2: "  Kumuha ng API key: console.anthropic.com → API Keys",
		ClaudeAPIHint3: "  Tandaan: hiwalay sa inyong Claude.ai Pro subscription.",

		Step3Title: "Hakbang 3/4: Directory access ng agent",
		Step3Note1: "Kailangan ng mga agent ng file access permissions (ipinasa bilang --add-dir sa Claude CLI).",
		Step3Note2: "Ang Tetora data directory (~/.tetora/) ay laging kasama.",
		DirOptions: [3]string{
			"Home directory (~/)",
			"Mga tiyak na directory (i-configure mamaya sa config.json)",
			"Tetora data lamang (~/.tetora/)",
		},
		DirInputPrompt: "Mga directory (pinaghiwalay ng kuwit)",

		Step4Title: "Hakbang 4/4: Ginagawa ang config...",

		CreateRoleOCPrompt:    "Maaaring i-import ang mga OpenClaw agent mamaya gamit ang: tetora import openclaw\n  Gumawa ng bagong agent ngayon? [y/N]:",
		CreateRolePrompt:      "Gumawa ng unang agent? [Y/n]:",
		RoleNamePrompt:        "Pangalan ng agent",
		ArchetypeTitle:        "Magsimula mula sa template?",
		ArchetypeBlank:        "Magsimula mula sa simula",
		ArchetypeChoosePrompt: "Pumili [1-%d]",
		SoulFilePrompt:        "Soul file path (blangko para sa template)",
		RoleModelPrompt:       "Agent model",
		RoleDescPrompt:        "Paglalarawan",
		RolePermPrompt:        "Permission mode (plan|acceptEdits|autoEdit|bypassPermissions)",
		RolePermInvalid:       "Hindi kilalang permission mode %q, gagamitin ang acceptEdits",
		RoleAdded:             "Naidagdag ang agent %q.",
		RoleError:             "Error sa pag-save ng agent: %v",

		SetDefaultAgentPrompt:   "Itakda ang %q bilang default agent? [Y/n]:",
		DefaultAgentSet:         "Default agent na-set sa %q.",
		AutoRouteDiscordPrompt: "Auto-route Discord channels sa %q? [Y/n]:",
		AutoRouteDiscordDone:   "Discord channels na-route sa %q.",
		AddAnotherRolePrompt:   "Magdagdag ng isa pang agent? [y/N]:",
		EnableSmartDispatch:    "I-enable ang smart dispatch (auto-route messages sa best agent)? [Y/n]:",
		SmartDispatchEnabled:   "Smart dispatch enabled.",

		ServiceInstallPrompt: "I-install bilang launchd service? [y/N]:",

		FinalConfig:   "Config:",
		FinalJobs:     "Jobs:",
		NextSteps:     "Mga susunod na hakbang:",
		NextDoctor:    "  tetora doctor      I-verify ang setup",
		NextStatus:    "  tetora status      Mabilis na pangkalahatang-ideya",
		NextServe:     "  tetora serve       Simulan ang daemon",
		NextDashboard: "  tetora dashboard   Buksan ang web UI",

		APITokenLabel: "API token:",
		APITokenNote:  "(I-save ang token na ito — kailangan para sa CLI/API access)",
	},

	"th": {
		LangPrompt: "เลือกภาษา:",

		ConfigExists:    "มีไฟล์ config อยู่แล้ว:",
		OverwritePrompt: "เขียนทับ? [y/N]:",
		Aborted:         "ยกเลิกแล้ว",

		OpenClawDetected:     "พบการติดตั้ง OpenClaw ที่",
		OpenClawImport:       "นำเข้า config พื้นฐานจาก OpenClaw? [Y/n]:",
		OpenClawImported:     "นำเข้า %d ฟิลด์ config แล้ว",
		OpenClawMigrateNote1: "สำหรับการย้าย workspace ทั้งหมด (roles, memory, skills ฯลฯ),",
		OpenClawMigrateNote2: "รันหลังการตั้งค่า: tetora import openclaw",

		Title: "=== ตั้งค่า Tetora อย่างรวดเร็ว ===",

		Step1Title: "ขั้นตอน 1/4: เลือกช่องทางส่งข้อความ",
		ChannelOptions: [4]string{
			"Telegram",
			"Discord",
			"Slack",
			"ข้ามขั้นตอน (ใช้เฉพาะ HTTP API)",
		},

		TelegramHint1:        "วิธีรับค่าเหล่านี้:",
		TelegramHint2:        "  1. ส่งข้อความถึง @BotFather ใน Telegram → /newbot",
		TelegramHint3:        "  2. คัดลอก bot token ที่ได้รับ",
		TelegramHint4:        "  3. ส่งข้อความถึง bot ของคุณ แล้วเข้าถึง:\n       https://api.telegram.org/bot<TOKEN>/getUpdates\n       เพื่อหา chat ID",
		TelegramTokenPrompt:  "Telegram bot token",
		TelegramChatIDPrompt: "Telegram chat ID",

		DiscordHint1:         "วิธีรับค่าเหล่านี้:",
		DiscordHint2:         "  1. ไปที่ https://discord.com/developers/applications",
		DiscordHint3:         "  2. สร้างแอปพลิเคชัน (หรือเลือกที่มีอยู่)",
		DiscordHint4:         "  3. Application ID → หน้า General Information (ด้านบน)\n  4. Bot → Reset Token → คัดลอก (นี่คือ bot token)\n  5. Bot → เลื่อนลง → เปิดใช้งาน MESSAGE CONTENT INTENT",
		DiscordHint5:         "  6. เชิญ bot เข้าเซิร์ฟเวอร์:",
		DiscordHint6:         "     (ยังไม่มีเซิร์ฟเวอร์? แถบด้านซ้าย Discord → '+' → สร้างของตัวเอง)\n     OAuth2 → URL Generator → ติ๊ก 'bot' ใน SCOPES\n     → ติ๊กสิทธิ์ (Send Messages, Read Message History)\n     → คัดลอก Generated URL ด้านล่าง → เปิดในเบราว์เซอร์ → เลือกเซิร์ฟเวอร์",
		DiscordHint7:         "  7. รับ channel ID:\n     Discord → การตั้งค่า (ไอคอนเฟืองใกล้ชื่อผู้ใช้)\n     → การตั้งค่าแอป → ขั้นสูง → เปิด Developer Mode\n     → กลับไป คลิกขวาที่ช่องเป้าหมาย → คัดลอก ID ช่อง",
		DiscordTokenPrompt:   "Discord bot token",
		DiscordAppIDPrompt:   "Discord application ID",
		DiscordChannelPrompt: "Discord channel ID",

		SlackHint1:               "วิธีรับค่าเหล่านี้:",
		SlackHint2:               "  1. ไปที่ https://api.slack.com/apps → Create New App",
		SlackHint3:               "  2. Bot token → OAuth & Permissions → ติดตั้งใน Workspace\n     → คัดลอก token xoxb-...\n  3. Signing secret → Basic Information → App Credentials",
		SlackTokenPrompt:         "Slack bot token (xoxb-...)",
		SlackSigningSecretPrompt: "Slack signing secret",

		Step2Title: "ขั้นตอน 2/4: เลือกผู้ให้บริการ AI",
		ProviderOptions: [3]string{
			"Claude CLI (ไบนารี claude ในเครื่อง)",
			"Claude API (ใช้ API key โดยตรง)",
			"API ที่รองรับ OpenAI",
		},
		ClaudeCLIPathPrompt:  "เส้นทาง Claude CLI",
		DefaultModelPrompt:   "โมเดลเริ่มต้น",
		ClaudeAPIKeyPrompt:   "Claude API key",
		OpenAIEndpointPrompt: "API endpoint",
		OpenAIKeyPrompt:      "API key",

		ClaudeCLIHint1: "แนะนำสำหรับสมาชิก Claude.ai Pro ($20/เดือน)",
		ClaudeCLIHint2: "  ต้องติดตั้ง Claude Code CLI บนเครื่องก่อน",
		ClaudeCLIHint3: "  ติดตั้ง: npm install -g @anthropic-ai/claude-code",
		ClaudeCLIHint4: "  เข้าสู่ระบบ: claude auth login   (ใช้บัญชี Claude.ai ของคุณ)",
		ClaudeCLIHint5: "  หมายเหตุ: มีขีดจำกัดการใช้งานรายเดือนตามแผนที่สมัคร",
		ClaudeAPIHint1: "ต้องมีบัญชี Claude API (คิดค่าใช้จ่ายตามการใช้งาน แยกจากสมาชิก)",
		ClaudeAPIHint2: "  รับ API key: console.anthropic.com → API Keys",
		ClaudeAPIHint3: "  หมายเหตุ: แยกจากการสมัครสมาชิก Claude.ai Pro ของคุณ",

		Step3Title: "ขั้นตอน 3/4: การเข้าถึงไดเรกทอรีของ agent",
		Step3Note1: "Agents ต้องการสิทธิ์เข้าถึงไฟล์ (ส่งเป็น --add-dir ไปยัง Claude CLI)",
		Step3Note2: "ไดเรกทอรีข้อมูล Tetora (~/.tetora/) รวมอยู่เสมอ",
		DirOptions: [3]string{
			"Home directory (~/)",
			"ไดเรกทอรีเฉพาะ (กำหนดค่าภายหลังใน config.json)",
			"เฉพาะข้อมูล Tetora (~/.tetora/)",
		},
		DirInputPrompt: "ไดเรกทอรี (คั่นด้วยเครื่องหมายจุลภาค)",

		Step4Title: "ขั้นตอน 4/4: กำลังสร้าง config...",

		CreateRoleOCPrompt:    "สามารถนำเข้า agents ของ OpenClaw ภายหลังด้วย: tetora import openclaw\n  สร้าง agent ใหม่ตอนนี้? [y/N]:",
		CreateRolePrompt:      "สร้าง agent แรก? [Y/n]:",
		RoleNamePrompt:        "ชื่อ agent",
		ArchetypeTitle:        "เริ่มจาก template?",
		ArchetypeBlank:        "เริ่มจากศูนย์",
		ArchetypeChoosePrompt: "เลือก [1-%d]",
		SoulFilePrompt:        "เส้นทาง Soul file (ว่างเปล่าเพื่อใช้ template)",
		RoleModelPrompt:       "โมเดลของ agent",
		RoleDescPrompt:        "คำอธิบาย",
		RolePermPrompt:        "โหมดสิทธิ์ (plan|acceptEdits|autoEdit|bypassPermissions)",
		RolePermInvalid:       "โหมดสิทธิ์ไม่รู้จัก %q ใช้ acceptEdits แทน",
		RoleAdded:             "เพิ่ม agent %q แล้ว",
		RoleError:             "เกิดข้อผิดพลาดในการบันทึก agent: %v",

		SetDefaultAgentPrompt:   "ตั้ง %q เป็นเอเจนต์เริ่มต้น? [Y/n]:",
		DefaultAgentSet:         "ตั้งค่าเอเจนต์เริ่มต้นเป็น %q แล้ว",
		AutoRouteDiscordPrompt: "เชื่อมต่อช่อง Discord ไปยัง %q อัตโนมัติ? [Y/n]:",
		AutoRouteDiscordDone:   "ช่อง Discord เชื่อมต่อไปยัง %q แล้ว",
		AddAnotherRolePrompt:   "เพิ่ม agent อื่นอีกไหม? [y/N]:",
		EnableSmartDispatch:    "เปิดใช้ smart dispatch (เชื่อมต่อข้อความไปยังเอเจนต์ที่เหมาะที่สุดอัตโนมัติ)? [Y/n]:",
		SmartDispatchEnabled:   "เปิดใช้ smart dispatch แล้ว",

		ServiceInstallPrompt: "ติดตั้งเป็น launchd service? [y/N]:",

		FinalConfig:   "Config:",
		FinalJobs:     "Jobs:",
		NextSteps:     "ขั้นตอนต่อไป:",
		NextDoctor:    "  tetora doctor      ตรวจสอบการตั้งค่า",
		NextStatus:    "  tetora status      ภาพรวมอย่างรวดเร็ว",
		NextServe:     "  tetora serve       เริ่ม daemon",
		NextDashboard: "  tetora dashboard   เปิด web UI",

		APITokenLabel: "API token:",
		APITokenNote:  "(บันทึก token นี้ไว้ — จำเป็นสำหรับการเข้าถึง CLI/API)",
	},
}
