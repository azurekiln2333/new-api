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
