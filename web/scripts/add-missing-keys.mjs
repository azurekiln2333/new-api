import fs from 'node:fs/promises'
import path from 'node:path'

// Temporary reviewed translation input. Execute only in the hosted locale job.
const LOCALES_DIR = path.resolve('src/i18n/locales')

function stableStringify(obj) {
  return JSON.stringify(obj, null, 2) + '\n'
}

const newKeys = {
  en: {
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
  },
  zh: {
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
    'fingerprint endpoint type is not supported':
      '不支持此指纹检测端点类型。',
  },
  'zh-TW': {
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
    'fingerprint endpoint type is not supported':
      '不支援此指紋偵測端點類型。',
  },
  fr: {
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
  },
  ja: {
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
  },
  ru: {
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
  },
  vi: {
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
  },
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
