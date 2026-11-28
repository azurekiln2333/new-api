import fs from 'node:fs/promises'
import path from 'node:path'

// Temporary reviewed translation input. Execute only in the hosted locale job.
const LOCALES_DIR = path.resolve('src/i18n/locales')

function stableStringify(obj) {
  return JSON.stringify(obj, null, 2) + '\n'
}

const newKeys = {
  en: {
    'Restore inheritance': 'Restore inheritance',
    'Invalid key model mapping': 'Invalid key model mapping',
    'Key mapping overrides the same source rule on the channel. Other rules inherit from the channel. Clear this list to restore inheritance.':
      'Key mapping overrides the same source rule on the channel. Other rules inherit from the channel. Clear this list to restore inheritance.',
    'Deployment default': 'Deployment default',
    'Leave blank to use the deployment default: environment setting, then build default, then {{repository}}.':
      'Leave blank to use the deployment default: environment setting, then build default, then {{repository}}.',
    'Load more': 'Load more',
    'Parsed numbers: {{count}}': 'Parsed numbers: {{count}}',
    'Change prompts': 'Change prompts',
    'Fingerprint preview is unavailable. Change prompts and try again.':
      'Fingerprint preview is unavailable. Change prompts and try again.',
    'Each prompt independently uses Chinese, English, Japanese, Korean, or French.':
      'Each prompt independently uses Chinese, English, Japanese, Korean, or French.',
    'The upstream provider rejected the request.':
      'The upstream provider rejected the request.',
    'The upstream provider reported a quota restriction.':
      'The upstream provider reported a quota restriction.',
    'The upstream provider is rate limiting requests.':
      'The upstream provider is rate limiting requests.',
    'The upstream provider is temporarily unavailable.':
      'The upstream provider is temporarily unavailable.',
    'The upstream provider returned an error.':
      'The upstream provider returned an error.',
    'a fingerprint preview is required': 'a fingerprint preview is required',
    'fingerprint preview is unavailable or expired':
      'fingerprint preview is unavailable or expired',
    'too many fingerprint previews; wait for one to expire':
      'too many fingerprint previews; wait for one to expire',
    'authenticated fingerprint owner is unavailable':
      'authenticated fingerprint owner is unavailable',
    'invalid fingerprint key selection': 'invalid fingerprint key selection',
    'the selected key is unavailable for this model':
      'the selected key is unavailable for this model',
    'fingerprint endpoint type is not supported':
      'fingerprint endpoint type is not supported',
    Postmortem: 'Postmortem',
    'No verified public status feed is available.':
      'No verified public status feed is available.',
    'The last successful fetch time is invalid.':
      'The last successful fetch time is invalid.',
    'The official feed request timed out.':
      'The official feed request timed out.',
    'The official feed hostname could not be resolved.':
      'The official feed hostname could not be resolved.',
    'The secure connection to the official feed failed.':
      'The secure connection to the official feed failed.',
    'The connection to the official feed could not be completed.':
      'The connection to the official feed could not be completed.',
    'The official feed returned an HTTP error.':
      'The official feed returned an HTTP error.',
    'The official feed response could not be validated.':
      'The official feed response could not be validated.',
    'The official feed address is invalid.':
      'The official feed address is invalid.',
    'The official feed redirect was rejected.':
      'The official feed redirect was rejected.',
    'The official feed response could not be read.':
      'The official feed response could not be read.',
    'The official feed response exceeded the size limit.':
      'The official feed response exceeded the size limit.',
    'Official feed data is unavailable.': 'Official feed data is unavailable.',
    'HTTP {{status}}': 'HTTP {{status}}',
    Fresh: 'Fresh',
    'Status data': 'Status data',
    'Last known status': 'Last known status',
    'No successful fetch yet.': 'No successful fetch yet.',
    Components: 'Components',
    'Active incidents': 'Active incidents',
    'No active incidents reported.': 'No active incidents reported.',
    'Active incident data is unavailable.':
      'Active incident data is unavailable.',
    'Recent incidents': 'Recent incidents',
    'Recent incident data is unavailable.':
      'Recent incident data is unavailable.',
    'No additional recent incidents reported.':
      'No additional recent incidents reported.',
    'History last successful fetch': 'History last successful fetch',
    'History last checked': 'History last checked',
    'Official feeds are cached for five minutes. Successful data older than thirty minutes is stale. Unverified feeds remain unavailable.':
      'Official feeds are cached for five minutes. Successful data older than thirty minutes is stale. Unverified feeds remain unavailable.',
    'Everyone except me': 'Everyone except me',
    'fingerprint request no longer matches the previewed prompt':
      'fingerprint request no longer matches the previewed prompt',
  },
  zh: {
    'Restore inheritance': '恢复继承',
    'Invalid key model mapping': '密钥模型映射无效',
    'Key mapping overrides the same source rule on the channel. Other rules inherit from the channel. Clear this list to restore inheritance.':
      '密钥映射会覆盖渠道中相同来源模型的规则，其他规则继承渠道配置。清空此列表可恢复继承。',
    'Deployment default': '部署默认值',
    'Leave blank to use the deployment default: environment setting, then build default, then {{repository}}.':
      '留空时使用部署默认值：依次采用环境设置、构建默认值，最后使用 {{repository}}。',
    'Load more': '加载更多',
    'Parsed numbers: {{count}}': '已解析 {{count}} 个数字',
    'Change prompts': '换一组提示词',
    'Fingerprint preview is unavailable. Change prompts and try again.':
      '指纹检测预览不可用，请换一组提示词后重试。',
    'Each prompt independently uses Chinese, English, Japanese, Korean, or French.':
      '每道提示词独立使用中文、英文、日文、韩文或法文。',
    'The upstream provider rejected the request.': '上游服务商拒绝了请求。',
    'The upstream provider reported a quota restriction.':
      '上游服务商报告了额度限制。',
    'The upstream provider is rate limiting requests.':
      '上游服务商正在限制请求频率。',
    'The upstream provider is temporarily unavailable.':
      '上游服务商暂时不可用。',
    'The upstream provider returned an error.': '上游服务商返回了错误。',
    'a fingerprint preview is required': '需要指纹检测预览。',
    'fingerprint preview is unavailable or expired':
      '指纹检测预览不可用或已过期。',
    'too many fingerprint previews; wait for one to expire':
      '指纹检测预览过多，请等待其中一个过期。',
    'authenticated fingerprint owner is unavailable':
      '无法获取指纹检测的已认证用户。',
    'invalid fingerprint key selection': '指纹检测的密钥选择无效。',
    'the selected key is unavailable for this model':
      '所选密钥无法用于此模型。',
    'fingerprint endpoint type is not supported': '不支持此指纹检测端点类型。',
    Postmortem: '事后分析',
    'No verified public status feed is available.':
      '暂无经过验证的公开状态数据源。',
    'The last successful fetch time is invalid.':
      '上次成功获取数据的时间无效。',
    'The official feed request timed out.': '官方数据源请求超时。',
    'The official feed hostname could not be resolved.':
      '无法解析官方数据源的主机名。',
    'The secure connection to the official feed failed.':
      '与官方数据源的安全连接失败。',
    'The connection to the official feed could not be completed.':
      '无法完成与官方数据源的连接。',
    'The official feed returned an HTTP error.': '官方数据源返回了 HTTP 错误。',
    'The official feed response could not be validated.':
      '无法验证官方数据源的响应。',
    'The official feed address is invalid.': '官方数据源地址无效。',
    'The official feed redirect was rejected.': '官方数据源的重定向被拒绝。',
    'The official feed response could not be read.':
      '无法读取官方数据源的响应。',
    'The official feed response exceeded the size limit.':
      '官方数据源的响应超过大小限制。',
    'Official feed data is unavailable.': '官方数据源的数据不可用。',
    'HTTP {{status}}': 'HTTP {{status}}',
    Fresh: '新近',
    'Status data': '状态数据',
    'Last known status': '最近已知状态',
    'No successful fetch yet.': '尚未成功获取数据。',
    Components: '服务组件',
    'Active incidents': '进行中的事件',
    'No active incidents reported.': '未报告进行中的事件。',
    'Active incident data is unavailable.': '进行中的事件数据不可用。',
    'Recent incidents': '近期事件',
    'Recent incident data is unavailable.': '近期事件数据不可用。',
    'No additional recent incidents reported.': '未报告其他近期事件。',
    'History last successful fetch': '历史记录上次成功获取时间',
    'History last checked': '历史记录上次检查时间',
    'Official feeds are cached for five minutes. Successful data older than thirty minutes is stale. Unverified feeds remain unavailable.':
      '官方数据源缓存五分钟。成功获取的数据超过三十分钟后视为过时。未经验证的数据源保持不可用。',
    'Everyone except me': '除我以外的所有人',
    'fingerprint request no longer matches the previewed prompt':
      '指纹检测请求与预览的提示词不再匹配。',
  },
  'zh-TW': {
    'Restore inheritance': '恢復繼承',
    'Invalid key model mapping': '金鑰模型對應無效',
    'Key mapping overrides the same source rule on the channel. Other rules inherit from the channel. Clear this list to restore inheritance.':
      '金鑰對應會覆寫通道中相同來源模型的規則，其他規則繼承通道設定。清除此清單可恢復繼承。',
    'Deployment default': '部署預設值',
    'Leave blank to use the deployment default: environment setting, then build default, then {{repository}}.':
      '留空時使用部署預設值：依序採用環境設定、建置預設值，最後使用 {{repository}}。',
    'Load more': '載入更多',
    'Parsed numbers: {{count}}': '已解析 {{count}} 個數字',
    'Change prompts': '換一組提示詞',
    'Fingerprint preview is unavailable. Change prompts and try again.':
      '指紋偵測預覽無法使用，請換一組提示詞後重試。',
    'Each prompt independently uses Chinese, English, Japanese, Korean, or French.':
      '每道提示詞獨立使用中文、英文、日文、韓文或法文。',
    'The upstream provider rejected the request.': '上游服務商拒絕了請求。',
    'The upstream provider reported a quota restriction.':
      '上游服務商回報了額度限制。',
    'The upstream provider is rate limiting requests.':
      '上游服務商正在限制請求頻率。',
    'The upstream provider is temporarily unavailable.':
      '上游服務商暫時無法使用。',
    'The upstream provider returned an error.': '上游服務商回傳了錯誤。',
    'a fingerprint preview is required': '需要指紋偵測預覽。',
    'fingerprint preview is unavailable or expired':
      '指紋偵測預覽無法使用或已過期。',
    'too many fingerprint previews; wait for one to expire':
      '指紋偵測預覽過多，請等待其中一個過期。',
    'authenticated fingerprint owner is unavailable':
      '無法取得指紋偵測的已驗證使用者。',
    'invalid fingerprint key selection': '指紋偵測的金鑰選擇無效。',
    'the selected key is unavailable for this model':
      '所選金鑰無法用於此模型。',
    'fingerprint endpoint type is not supported': '不支援此指紋偵測端點類型。',
    Postmortem: '事後分析',
    'No verified public status feed is available.':
      '目前沒有經過驗證的公開狀態資料來源。',
    'The last successful fetch time is invalid.':
      '上次成功取得資料的時間無效。',
    'The official feed request timed out.': '官方資料來源請求逾時。',
    'The official feed hostname could not be resolved.':
      '無法解析官方資料來源的主機名稱。',
    'The secure connection to the official feed failed.':
      '與官方資料來源的安全連線失敗。',
    'The connection to the official feed could not be completed.':
      '無法完成與官方資料來源的連線。',
    'The official feed returned an HTTP error.':
      '官方資料來源回傳了 HTTP 錯誤。',
    'The official feed response could not be validated.':
      '無法驗證官方資料來源的回應。',
    'The official feed address is invalid.': '官方資料來源位址無效。',
    'The official feed redirect was rejected.': '官方資料來源的重新導向遭拒。',
    'The official feed response could not be read.':
      '無法讀取官方資料來源的回應。',
    'The official feed response exceeded the size limit.':
      '官方資料來源的回應超過大小限制。',
    'Official feed data is unavailable.': '官方資料來源的資料無法使用。',
    'HTTP {{status}}': 'HTTP {{status}}',
    Fresh: '新近',
    'Status data': '狀態資料',
    'Last known status': '最近已知狀態',
    'No successful fetch yet.': '尚未成功取得資料。',
    Components: '服務元件',
    'Active incidents': '進行中的事件',
    'No active incidents reported.': '未回報進行中的事件。',
    'Active incident data is unavailable.': '進行中的事件資料無法使用。',
    'Recent incidents': '近期事件',
    'Recent incident data is unavailable.': '近期事件資料無法使用。',
    'No additional recent incidents reported.': '未回報其他近期事件。',
    'History last successful fetch': '歷史紀錄上次成功取得時間',
    'History last checked': '歷史紀錄上次檢查時間',
    'Official feeds are cached for five minutes. Successful data older than thirty minutes is stale. Unverified feeds remain unavailable.':
      '官方資料來源快取五分鐘。成功取得的資料超過三十分鐘後視為過時。未經驗證的資料來源維持無法使用。',
    'Everyone except me': '除我以外的所有人',
    'fingerprint request no longer matches the previewed prompt':
      '指紋偵測請求與預覽的提示詞不再相符。',
  },
  fr: {
    'Restore inheritance': 'Rétablir l’héritage',
    'Invalid key model mapping': 'Correspondance de modèles de la clé invalide',
    'Key mapping overrides the same source rule on the channel. Other rules inherit from the channel. Clear this list to restore inheritance.':
      'La correspondance de la clé remplace la règle du canal pour le même modèle source. Les autres règles sont héritées du canal. Videz cette liste pour rétablir l’héritage.',
    'Deployment default': 'Valeur du déploiement',
    'Leave blank to use the deployment default: environment setting, then build default, then {{repository}}.':
      'Laissez vide pour utiliser la valeur du déploiement : paramètre d’environnement, puis valeur de compilation, puis {{repository}}.',
    'Load more': 'Charger plus',
    'Parsed numbers: {{count}}': 'Nombres reconnus : {{count}}',
    'Change prompts': 'Changer les instructions',
    'Fingerprint preview is unavailable. Change prompts and try again.':
      'L’aperçu de détection est indisponible. Changez les instructions et réessayez.',
    'Each prompt independently uses Chinese, English, Japanese, Korean, or French.':
      'Chaque instruction utilise indépendamment le chinois, l’anglais, le japonais, le coréen ou le français.',
    'The upstream provider rejected the request.':
      'Le fournisseur en amont a rejeté la requête.',
    'The upstream provider reported a quota restriction.':
      'Le fournisseur en amont a signalé une restriction de quota.',
    'The upstream provider is rate limiting requests.':
      'Le fournisseur en amont limite la fréquence des requêtes.',
    'The upstream provider is temporarily unavailable.':
      'Le fournisseur en amont est temporairement indisponible.',
    'The upstream provider returned an error.':
      'Le fournisseur en amont a renvoyé une erreur.',
    'a fingerprint preview is required':
      'Un aperçu de détection d’empreinte est requis.',
    'fingerprint preview is unavailable or expired':
      'L’aperçu de détection d’empreinte est indisponible ou a expiré.',
    'too many fingerprint previews; wait for one to expire':
      'Trop d’aperçus de détection d’empreinte. Attendez que l’un d’eux expire.',
    'authenticated fingerprint owner is unavailable':
      'L’utilisateur authentifié associé à la détection est indisponible.',
    'invalid fingerprint key selection':
      'La clé sélectionnée pour la détection d’empreinte est invalide.',
    'the selected key is unavailable for this model':
      'La clé sélectionnée n’est pas disponible pour ce modèle.',
    'fingerprint endpoint type is not supported':
      'Ce type de point de terminaison de détection d’empreinte n’est pas pris en charge.',
    Postmortem: 'Analyse après incident',
    'No verified public status feed is available.':
      'Aucun flux public d’état vérifié n’est disponible.',
    'The last successful fetch time is invalid.':
      'L’heure de la dernière récupération réussie est invalide.',
    'The official feed request timed out.':
      'Le délai de la requête au flux officiel a expiré.',
    'The official feed hostname could not be resolved.':
      'Le nom d’hôte du flux officiel n’a pas pu être résolu.',
    'The secure connection to the official feed failed.':
      'La connexion sécurisée au flux officiel a échoué.',
    'The connection to the official feed could not be completed.':
      'La connexion au flux officiel n’a pas pu être établie.',
    'The official feed returned an HTTP error.':
      'Le flux officiel a renvoyé une erreur HTTP.',
    'The official feed response could not be validated.':
      'La réponse du flux officiel n’a pas pu être validée.',
    'The official feed address is invalid.':
      'L’adresse du flux officiel est invalide.',
    'The official feed redirect was rejected.':
      'La redirection du flux officiel a été refusée.',
    'The official feed response could not be read.':
      'La réponse du flux officiel n’a pas pu être lue.',
    'The official feed response exceeded the size limit.':
      'La réponse du flux officiel dépasse la taille limite.',
    'Official feed data is unavailable.':
      'Les données du flux officiel sont indisponibles.',
    'HTTP {{status}}': 'HTTP {{status}}',
    Fresh: 'Récentes',
    'Status data': 'Données d’état',
    'Last known status': 'Dernier état connu',
    'No successful fetch yet.': 'Aucune récupération réussie pour le moment.',
    Components: 'Composants',
    'Active incidents': 'Incidents en cours',
    'No active incidents reported.': 'Aucun incident en cours signalé.',
    'Active incident data is unavailable.':
      'Les données des incidents en cours sont indisponibles.',
    'Recent incidents': 'Incidents récents',
    'Recent incident data is unavailable.':
      'Les données des incidents récents sont indisponibles.',
    'No additional recent incidents reported.':
      'Aucun autre incident récent signalé.',
    'History last successful fetch':
      'Dernière récupération réussie de l’historique',
    'History last checked': 'Dernière vérification de l’historique',
    'Official feeds are cached for five minutes. Successful data older than thirty minutes is stale. Unverified feeds remain unavailable.':
      'Les flux officiels sont mis en cache pendant cinq minutes. Les données récupérées avec succès il y a plus de trente minutes sont périmées. Les flux non vérifiés restent indisponibles.',
    'Everyone except me': 'Tout le monde sauf moi',
    'fingerprint request no longer matches the previewed prompt':
      'La requête de détection d’empreinte ne correspond plus à l’instruction prévisualisée.',
  },
  ja: {
    'Restore inheritance': '継承に戻す',
    'Invalid key model mapping': 'キーのモデルマッピングが無効です',
    'Key mapping overrides the same source rule on the channel. Other rules inherit from the channel. Clear this list to restore inheritance.':
      'キーのマッピングは、同じ元モデルに対するチャネルのルールを上書きします。他のルールはチャネルから継承されます。このリストを空にすると継承に戻ります。',
    'Deployment default': 'デプロイの既定値',
    'Leave blank to use the deployment default: environment setting, then build default, then {{repository}}.':
      '空欄の場合、環境設定、ビルド時の既定値、{{repository}} の順に適用します。',
    'Load more': 'さらに読み込む',
    'Parsed numbers: {{count}}': '解析済みの数値：{{count}} 個',
    'Change prompts': 'プロンプトを変更',
    'Fingerprint preview is unavailable. Change prompts and try again.':
      'フィンガープリント検出のプレビューを利用できません。プロンプトを変更して再試行してください。',
    'Each prompt independently uses Chinese, English, Japanese, Korean, or French.':
      '各プロンプトは個別に中国語、英語、日本語、韓国語、フランス語のいずれかを使用します。',
    'The upstream provider rejected the request.':
      '上流プロバイダーがリクエストを拒否しました。',
    'The upstream provider reported a quota restriction.':
      '上流プロバイダーからクォータ制限が報告されました。',
    'The upstream provider is rate limiting requests.':
      '上流プロバイダーがリクエストの頻度を制限しています。',
    'The upstream provider is temporarily unavailable.':
      '上流プロバイダーは一時的に利用できません。',
    'The upstream provider returned an error.':
      '上流プロバイダーがエラーを返しました。',
    'a fingerprint preview is required':
      'フィンガープリント検出のプレビューが必要です。',
    'fingerprint preview is unavailable or expired':
      'フィンガープリント検出のプレビューを利用できないか、有効期限が切れています。',
    'too many fingerprint previews; wait for one to expire':
      'フィンガープリント検出のプレビューが多すぎます。いずれかの有効期限が切れるまでお待ちください。',
    'authenticated fingerprint owner is unavailable':
      'フィンガープリント検出の認証済みユーザーを取得できません。',
    'invalid fingerprint key selection':
      'フィンガープリント検出のキー選択が無効です。',
    'the selected key is unavailable for this model':
      '選択したキーはこのモデルでは利用できません。',
    'fingerprint endpoint type is not supported':
      'このフィンガープリント検出のエンドポイント種別には対応していません。',
    Postmortem: '事後分析',
    'No verified public status feed is available.':
      '確認済みの公開ステータスフィードはありません。',
    'The last successful fetch time is invalid.':
      '最後に取得に成功した時刻が無効です。',
    'The official feed request timed out.':
      '公式フィードへのリクエストがタイムアウトしました。',
    'The official feed hostname could not be resolved.':
      '公式フィードのホスト名を解決できませんでした。',
    'The secure connection to the official feed failed.':
      '公式フィードへの安全な接続に失敗しました。',
    'The connection to the official feed could not be completed.':
      '公式フィードへの接続を完了できませんでした。',
    'The official feed returned an HTTP error.':
      '公式フィードが HTTP エラーを返しました。',
    'The official feed response could not be validated.':
      '公式フィードの応答を検証できませんでした。',
    'The official feed address is invalid.':
      '公式フィードのアドレスが無効です。',
    'The official feed redirect was rejected.':
      '公式フィードのリダイレクトが拒否されました。',
    'The official feed response could not be read.':
      '公式フィードの応答を読み取れませんでした。',
    'The official feed response exceeded the size limit.':
      '公式フィードの応答がサイズ制限を超えました。',
    'Official feed data is unavailable.':
      '公式フィードのデータを利用できません。',
    'HTTP {{status}}': 'HTTP {{status}}',
    Fresh: '新しい',
    'Status data': 'ステータスデータ',
    'Last known status': '最後に確認された状態',
    'No successful fetch yet.': 'まだデータの取得に成功していません。',
    Components: 'コンポーネント',
    'Active incidents': '対応中のインシデント',
    'No active incidents reported.':
      '対応中のインシデントは報告されていません。',
    'Active incident data is unavailable.':
      '対応中のインシデントのデータを利用できません。',
    'Recent incidents': '最近のインシデント',
    'Recent incident data is unavailable.':
      '最近のインシデントのデータを利用できません。',
    'No additional recent incidents reported.':
      'ほかに最近のインシデントは報告されていません。',
    'History last successful fetch': '履歴の最終取得成功日時',
    'History last checked': '履歴の最終確認日時',
    'Official feeds are cached for five minutes. Successful data older than thirty minutes is stale. Unverified feeds remain unavailable.':
      '公式フィードは5分間キャッシュされます。取得に成功してから30分を超えたデータは古いデータとして扱われます。未確認のフィードは利用できません。',
    'Everyone except me': '自分以外の全員',
    'fingerprint request no longer matches the previewed prompt':
      'フィンガープリント検出のリクエストがプレビューしたプロンプトと一致しなくなりました。',
  },
  ru: {
    'Restore inheritance': 'Восстановить наследование',
    'Invalid key model mapping': 'Недопустимое сопоставление моделей ключа',
    'Key mapping overrides the same source rule on the channel. Other rules inherit from the channel. Clear this list to restore inheritance.':
      'Сопоставление ключа заменяет правило канала для той же исходной модели. Остальные правила наследуются от канала. Очистите список, чтобы восстановить наследование.',
    'Deployment default': 'По умолчанию для установки',
    'Leave blank to use the deployment default: environment setting, then build default, then {{repository}}.':
      'Оставьте пустым для значения по умолчанию: сначала настройка окружения, затем значение сборки, затем {{repository}}.',
    'Load more': 'Загрузить ещё',
    'Parsed numbers: {{count}}': 'Распознано чисел: {{count}}',
    'Change prompts': 'Сменить промпты',
    'Fingerprint preview is unavailable. Change prompts and try again.':
      'Предпросмотр проверки отпечатка недоступен. Смените промпты и повторите попытку.',
    'Each prompt independently uses Chinese, English, Japanese, Korean, or French.':
      'Каждый промпт независимо использует китайский, английский, японский, корейский или французский язык.',
    'The upstream provider rejected the request.':
      'Вышестоящий провайдер отклонил запрос.',
    'The upstream provider reported a quota restriction.':
      'Вышестоящий провайдер сообщил об ограничении квоты.',
    'The upstream provider is rate limiting requests.':
      'Вышестоящий провайдер ограничивает частоту запросов.',
    'The upstream provider is temporarily unavailable.':
      'Вышестоящий провайдер временно недоступен.',
    'The upstream provider returned an error.':
      'Вышестоящий провайдер вернул ошибку.',
    'a fingerprint preview is required':
      'Для проверки отпечатка требуется предпросмотр.',
    'fingerprint preview is unavailable or expired':
      'Предпросмотр проверки отпечатка недоступен или срок его действия истёк.',
    'too many fingerprint previews; wait for one to expire':
      'Слишком много предпросмотров проверки отпечатка. Дождитесь истечения одного из них.',
    'authenticated fingerprint owner is unavailable':
      'Аутентифицированный владелец проверки отпечатка недоступен.',
    'invalid fingerprint key selection':
      'Недопустимый выбор ключа для проверки отпечатка.',
    'the selected key is unavailable for this model':
      'Выбранный ключ недоступен для этой модели.',
    'fingerprint endpoint type is not supported':
      'Тип конечной точки проверки отпечатка не поддерживается.',
    Postmortem: 'Анализ инцидента',
    'No verified public status feed is available.':
      'Нет доступного проверенного публичного источника данных о состоянии.',
    'The last successful fetch time is invalid.':
      'Некорректное время последнего успешного получения данных.',
    'The official feed request timed out.':
      'Истекло время ожидания ответа официального источника.',
    'The official feed hostname could not be resolved.':
      'Не удалось разрешить имя хоста официального источника.',
    'The secure connection to the official feed failed.':
      'Не удалось установить защищённое соединение с официальным источником.',
    'The connection to the official feed could not be completed.':
      'Не удалось установить соединение с официальным источником.',
    'The official feed returned an HTTP error.':
      'Официальный источник вернул ошибку HTTP.',
    'The official feed response could not be validated.':
      'Не удалось проверить ответ официального источника.',
    'The official feed address is invalid.':
      'Некорректный адрес официального источника.',
    'The official feed redirect was rejected.':
      'Перенаправление официального источника отклонено.',
    'The official feed response could not be read.':
      'Не удалось прочитать ответ официального источника.',
    'The official feed response exceeded the size limit.':
      'Ответ официального источника превысил ограничение размера.',
    'Official feed data is unavailable.':
      'Данные официального источника недоступны.',
    'HTTP {{status}}': 'HTTP {{status}}',
    Fresh: 'Свежие',
    'Status data': 'Данные о состоянии',
    'Last known status': 'Последнее известное состояние',
    'No successful fetch yet.': 'Данные ещё не удалось получить.',
    Components: 'Компоненты',
    'Active incidents': 'Текущие инциденты',
    'No active incidents reported.': 'О текущих инцидентах не сообщается.',
    'Active incident data is unavailable.':
      'Данные о текущих инцидентах недоступны.',
    'Recent incidents': 'Недавние инциденты',
    'Recent incident data is unavailable.':
      'Данные о недавних инцидентах недоступны.',
    'No additional recent incidents reported.':
      'О других недавних инцидентах не сообщается.',
    'History last successful fetch': 'Последнее успешное получение истории',
    'History last checked': 'Последняя проверка истории',
    'Official feeds are cached for five minutes. Successful data older than thirty minutes is stale. Unverified feeds remain unavailable.':
      'Официальные источники кэшируются на пять минут. Успешно полученные данные старше тридцати минут считаются устаревшими. Непроверенные источники остаются недоступными.',
    'Everyone except me': 'Все, кроме меня',
    'fingerprint request no longer matches the previewed prompt':
      'Запрос проверки отпечатка больше не соответствует промпту из предпросмотра.',
  },
  vi: {
    'Restore inheritance': 'Khôi phục kế thừa',
    'Invalid key model mapping': 'Ánh xạ mô hình của khóa không hợp lệ',
    'Key mapping overrides the same source rule on the channel. Other rules inherit from the channel. Clear this list to restore inheritance.':
      'Ánh xạ của khóa ghi đè quy tắc của kênh cho cùng mô hình nguồn. Các quy tắc khác được kế thừa từ kênh. Xóa danh sách này để khôi phục kế thừa.',
    'Deployment default': 'Mặc định của bản triển khai',
    'Leave blank to use the deployment default: environment setting, then build default, then {{repository}}.':
      'Để trống để dùng mặc định của bản triển khai: thiết lập môi trường, rồi mặc định khi biên dịch, cuối cùng là {{repository}}.',
    'Load more': 'Tải thêm',
    'Parsed numbers: {{count}}': 'Số đã phân tích: {{count}}',
    'Change prompts': 'Đổi bộ câu lệnh',
    'Fingerprint preview is unavailable. Change prompts and try again.':
      'Không thể xem trước phép kiểm tra dấu vân tay. Hãy đổi bộ câu lệnh rồi thử lại.',
    'Each prompt independently uses Chinese, English, Japanese, Korean, or French.':
      'Mỗi câu lệnh dùng độc lập một trong các ngôn ngữ: Trung, Anh, Nhật, Hàn hoặc Pháp.',
    'The upstream provider rejected the request.':
      'Nhà cung cấp thượng nguồn đã từ chối yêu cầu.',
    'The upstream provider reported a quota restriction.':
      'Nhà cung cấp thượng nguồn báo có giới hạn hạn mức.',
    'The upstream provider is rate limiting requests.':
      'Nhà cung cấp thượng nguồn đang giới hạn tần suất yêu cầu.',
    'The upstream provider is temporarily unavailable.':
      'Nhà cung cấp thượng nguồn tạm thời không khả dụng.',
    'The upstream provider returned an error.':
      'Nhà cung cấp thượng nguồn đã trả về lỗi.',
    'a fingerprint preview is required':
      'Cần có bản xem trước kiểm tra dấu vân tay.',
    'fingerprint preview is unavailable or expired':
      'Bản xem trước kiểm tra dấu vân tay không khả dụng hoặc đã hết hạn.',
    'too many fingerprint previews; wait for one to expire':
      'Có quá nhiều bản xem trước kiểm tra dấu vân tay. Hãy chờ một bản hết hạn.',
    'authenticated fingerprint owner is unavailable':
      'Chủ sở hữu đã xác thực của phép kiểm tra dấu vân tay không khả dụng.',
    'invalid fingerprint key selection':
      'Lựa chọn khóa cho phép kiểm tra dấu vân tay không hợp lệ.',
    'the selected key is unavailable for this model':
      'Khóa đã chọn không dùng được cho mô hình này.',
    'fingerprint endpoint type is not supported':
      'Loại điểm cuối kiểm tra dấu vân tay không được hỗ trợ.',
    Postmortem: 'Phân tích sau sự cố',
    'No verified public status feed is available.':
      'Không có nguồn dữ liệu trạng thái công khai đã được xác minh.',
    'The last successful fetch time is invalid.':
      'Thời điểm lấy dữ liệu thành công gần nhất không hợp lệ.',
    'The official feed request timed out.':
      'Yêu cầu tới nguồn dữ liệu chính thức đã hết thời gian chờ.',
    'The official feed hostname could not be resolved.':
      'Không thể phân giải tên máy chủ của nguồn dữ liệu chính thức.',
    'The secure connection to the official feed failed.':
      'Kết nối bảo mật tới nguồn dữ liệu chính thức thất bại.',
    'The connection to the official feed could not be completed.':
      'Không thể hoàn tất kết nối tới nguồn dữ liệu chính thức.',
    'The official feed returned an HTTP error.':
      'Nguồn dữ liệu chính thức trả về lỗi HTTP.',
    'The official feed response could not be validated.':
      'Không thể xác thực phản hồi từ nguồn dữ liệu chính thức.',
    'The official feed address is invalid.':
      'Địa chỉ nguồn dữ liệu chính thức không hợp lệ.',
    'The official feed redirect was rejected.':
      'Chuyển hướng của nguồn dữ liệu chính thức đã bị từ chối.',
    'The official feed response could not be read.':
      'Không thể đọc phản hồi từ nguồn dữ liệu chính thức.',
    'The official feed response exceeded the size limit.':
      'Phản hồi từ nguồn dữ liệu chính thức vượt quá giới hạn kích thước.',
    'Official feed data is unavailable.':
      'Dữ liệu từ nguồn chính thức không khả dụng.',
    'HTTP {{status}}': 'HTTP {{status}}',
    Fresh: 'Còn mới',
    'Status data': 'Dữ liệu trạng thái',
    'Last known status': 'Trạng thái ghi nhận gần nhất',
    'No successful fetch yet.': 'Chưa lấy dữ liệu thành công lần nào.',
    Components: 'Thành phần',
    'Active incidents': 'Sự cố đang diễn ra',
    'No active incidents reported.': 'Không có báo cáo về sự cố đang diễn ra.',
    'Active incident data is unavailable.':
      'Dữ liệu sự cố đang diễn ra không khả dụng.',
    'Recent incidents': 'Sự cố gần đây',
    'Recent incident data is unavailable.':
      'Dữ liệu sự cố gần đây không khả dụng.',
    'No additional recent incidents reported.':
      'Không có báo cáo về sự cố gần đây nào khác.',
    'History last successful fetch': 'Lần lấy lịch sử thành công gần nhất',
    'History last checked': 'Lần kiểm tra lịch sử gần nhất',
    'Official feeds are cached for five minutes. Successful data older than thirty minutes is stale. Unverified feeds remain unavailable.':
      'Nguồn dữ liệu chính thức được lưu đệm trong năm phút. Dữ liệu được lấy thành công cách đây hơn ba mươi phút được coi là cũ. Các nguồn chưa được xác minh vẫn không khả dụng.',
    'Everyone except me': 'Mọi người trừ tôi',
    'fingerprint request no longer matches the previewed prompt':
      'Yêu cầu kiểm tra dấu vân tay không còn khớp với câu lệnh đã xem trước.',
  },
}

const casePolicyTranslations = {
  en: {
    'Case-insensitive model IDs': 'Case-insensitive model IDs',
    'Inherit site policy': 'Inherit site policy',
    'Exact model IDs take priority. Case variants must resolve to one authorized catalog spelling; mapping and upstream spelling stay unchanged.': 'Exact model IDs take priority. Case variants must resolve to one authorized catalog spelling; mapping and upstream spelling stay unchanged.',
    'Disabled by default. Exact model IDs take priority; only an unambiguous ASCII case variant may resolve. Channels can override this policy.': 'Disabled by default. Exact model IDs take priority; only an unambiguous ASCII case variant may resolve. Channels can override this policy.',
  },
  zh: {
    'Case-insensitive model IDs': '模型 ID 不区分大小写',
    'Inherit site policy': '继承站点策略',
    'Exact model IDs take priority. Case variants must resolve to one authorized catalog spelling; mapping and upstream spelling stay unchanged.': '优先精确匹配模型 ID。大小写变体必须唯一匹配到有权限访问的目录名称；映射规则和上游名称保持不变。',
    'Disabled by default. Exact model IDs take priority; only an unambiguous ASCII case variant may resolve. Channels can override this policy.': '默认关闭。优先精确匹配模型 ID，仅允许无歧义的 ASCII 大小写变体匹配。渠道可覆盖此策略。',
  },
  'zh-TW': {
    'Case-insensitive model IDs': '模型 ID 不區分大小寫',
    'Inherit site policy': '繼承網站原則',
    'Exact model IDs take priority. Case variants must resolve to one authorized catalog spelling; mapping and upstream spelling stay unchanged.': '優先精確比對模型 ID。大小寫變體必須唯一對應至有權存取的目錄名稱；對應規則和上游名稱保持不變。',
    'Disabled by default. Exact model IDs take priority; only an unambiguous ASCII case variant may resolve. Channels can override this policy.': '預設停用。優先精確比對模型 ID，僅允許無歧義的 ASCII 大小寫變體比對。渠道可覆寫此原則。',
  },
  fr: {
    'Case-insensitive model IDs': 'ID de modèle insensibles à la casse',
    'Inherit site policy': 'Hériter de la politique du site',
    'Exact model IDs take priority. Case variants must resolve to one authorized catalog spelling; mapping and upstream spelling stay unchanged.': 'Les ID exacts sont prioritaires. Une variante de casse doit correspondre à un seul nom autorisé du catalogue ; les correspondances et les noms en amont restent inchangés.',
    'Disabled by default. Exact model IDs take priority; only an unambiguous ASCII case variant may resolve. Channels can override this policy.': 'Désactivé par défaut. Les ID exacts sont prioritaires ; seules les variantes de casse ASCII non ambiguës sont acceptées. Les canaux peuvent remplacer cette politique.',
  },
  ja: {
    'Case-insensitive model IDs': 'モデル ID の大文字・小文字を区別しない',
    'Inherit site policy': 'サイトのポリシーを継承',
    'Exact model IDs take priority. Case variants must resolve to one authorized catalog spelling; mapping and upstream spelling stay unchanged.': 'モデル ID の完全一致を優先します。大文字・小文字の違いは、アクセスが許可されたカタログ名に一意に解決できる場合のみ認められます。マッピングと上流の表記は変更しません。',
    'Disabled by default. Exact model IDs take priority; only an unambiguous ASCII case variant may resolve. Channels can override this policy.': '既定では無効です。モデル ID の完全一致を優先し、曖昧さのない ASCII の大文字・小文字の違いのみ許容します。チャネルごとに上書きできます。',
  },
  ru: {
    'Case-insensitive model IDs': 'ID моделей без учёта регистра',
    'Inherit site policy': 'Наследовать политику сайта',
    'Exact model IDs take priority. Case variants must resolve to one authorized catalog spelling; mapping and upstream spelling stay unchanged.': 'Точные ID имеют приоритет. Вариант регистра должен однозначно соответствовать разрешённому имени в каталоге; сопоставления и имена у провайдера не меняются.',
    'Disabled by default. Exact model IDs take priority; only an unambiguous ASCII case variant may resolve. Channels can override this policy.': 'По умолчанию отключено. Точные ID имеют приоритет; допустимы только однозначные варианты регистра ASCII. Каналы могут переопределить политику.',
  },
  vi: {
    'Case-insensitive model IDs': 'ID mô hình không phân biệt hoa thường',
    'Inherit site policy': 'Kế thừa chính sách trang',
    'Exact model IDs take priority. Case variants must resolve to one authorized catalog spelling; mapping and upstream spelling stay unchanged.': 'Ưu tiên ID mô hình khớp chính xác. Biến thể hoa thường phải khớp duy nhất với tên được phép trong danh mục; ánh xạ và tên thượng nguồn không thay đổi.',
    'Disabled by default. Exact model IDs take priority; only an unambiguous ASCII case variant may resolve. Channels can override this policy.': 'Mặc định tắt. Ưu tiên ID mô hình khớp chính xác; chỉ chấp nhận biến thể hoa thường ASCII không mơ hồ. Kênh có thể ghi đè chính sách này.',
  },
}

for (const [locale, translations] of Object.entries(casePolicyTranslations)) {
  Object.assign(newKeys[locale], translations)
}

async function main() {
  let totalAdded = 0

  for (const [locale, trans] of Object.entries(newKeys)) {
    const filePath = path.join(LOCALES_DIR, `${locale}.json`)
    const json = JSON.parse(await fs.readFile(filePath, 'utf8'))

    let count = 0
    for (const [key, value] of Object.entries(trans)) {
      if (!Object.prototype.hasOwnProperty.call(json.translation, key)) {
        json.translation[key] = value
        count++
      } else if (json.translation[key] !== value) {
        json.translation[key] = value
        count++
      }
    }

    if (count > 0) {
      json.translation = Object.fromEntries(
        Object.entries(json.translation).sort(([a], [b]) => a.localeCompare(b))
      )
      await fs.writeFile(filePath, stableStringify(json), 'utf8')
    }

    console.log(`${locale}: ${count} translations applied`)
    totalAdded += count
  }

  console.log(`\nTotal: ${totalAdded} translations applied`)
}

main().catch((err) => {
  console.error(err)
  process.exitCode = 1
})
