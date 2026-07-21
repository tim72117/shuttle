import { Star, MapPin, ImageOff } from 'lucide-react'

// 推薦景點卡片(UI 試做,假資料展示用)。
//
// 資料形狀對齊後端 want 工具 recommend_nearby(server/internal/wanttools/recommend_nearby.go)
// 目前回傳的欄位(name/address/lat/lng/rating/userRatingCount/primaryType)。
// photoUrl、summary 兩個欄位後端還沒接(Places API New 的 photos / editorialSummary),
// 先在型別上留好位置,UI 做好「沒有這兩個欄位時」的空狀態處理,之後後端補上就能直接吃。
export type RecommendedPlace = {
  name: string
  address: string
  lat: number
  lng: number
  rating: number // 0 表示無評分
  userRatingCount: number
  primaryType: string // 如 "tourist_attraction"、"restaurant"、"cafe"
  photoUrl?: string // 之後才有,先用假圖或漸層色塊佔位
  summary?: string // 景點介紹文字,之後才有,先寫假範例
}

// primaryType(Google Places 英文代碼)→ 中文標籤,對照 recommend_nearby.go 的 categoryToPlaceType。
const TYPE_LABELS: Record<string, string> = {
  tourist_attraction: '觀光景點',
  museum: '博物館',
  art_gallery: '美術館',
  restaurant: '餐廳',
  cafe: '咖啡廳',
  lodging: '住宿',
  park: '公園',
  shopping_mall: '購物',
  night_club: '夜生活',
  bar: '酒吧',
}

function typeLabel(primaryType: string): string {
  if (!primaryType) return '其他'
  return TYPE_LABELS[primaryType] ?? primaryType
}

// 依景點名稱挑一個穩定的漸層(同一張卡每次渲染顏色一致),當作沒有 photoUrl 時的佔位視覺。
const PLACEHOLDER_GRADIENTS = [
  'linear-gradient(135deg, #C4956A, #7C6F5B)',
  'linear-gradient(135deg, #5A8A6A, #9E9488)',
  'linear-gradient(135deg, #C0604A, #C4956A)',
  'linear-gradient(135deg, #7C6F5B, #3D3530)',
]

function placeholderGradient(seed: string): string {
  let hash = 0
  for (let i = 0; i < seed.length; i++) hash = (hash * 31 + seed.charCodeAt(i)) | 0
  const idx = Math.abs(hash) % PLACEHOLDER_GRADIENTS.length
  return PLACEHOLDER_GRADIENTS[idx]
}

// 星星評分:滿分 5 顆,無評分(rating === 0)時不畫星星,改顯示「尚無評分」文字,
// 避免出現 0 顆星或 NaN 之類看起來像 bug 的畫面。
function RatingStars({ rating, userRatingCount }: { rating: number; userRatingCount: number }) {
  if (!rating || rating <= 0) {
    return <span className="rp-rating rp-rating-empty">尚無評分</span>
  }
  const rounded = Math.round(rating)
  return (
    <span className="rp-rating">
      <span className="rp-stars" aria-hidden="true">
        {Array.from({ length: 5 }, (_, i) => (
          <Star
            key={i}
            size={13}
            strokeWidth={1.5}
            className={i < rounded ? 'rp-star filled' : 'rp-star'}
          />
        ))}
      </span>
      <span className="rp-rating-value">{rating.toFixed(1)}</span>
      {userRatingCount > 0 && (
        <span className="rp-rating-count">({userRatingCount.toLocaleString('zh-Hant')})</span>
      )}
    </span>
  )
}

export function RecommendedPlaceCard({ place }: { place: RecommendedPlace }) {
  return (
    <div className="recommended-place-card">
      <div className="rp-photo" style={place.photoUrl ? undefined : { background: placeholderGradient(place.name) }}>
        {place.photoUrl ? (
          <img src={place.photoUrl} alt={place.name} loading="lazy" />
        ) : (
          <div className="rp-photo-placeholder">
            <ImageOff size={20} strokeWidth={1.5} />
            <span>佔位圖</span>
          </div>
        )}
        <span className="rp-type-badge">{typeLabel(place.primaryType)}</span>
      </div>
      <div className="rp-body">
        <div className="rp-name">{place.name}</div>
        <RatingStars rating={place.rating} userRatingCount={place.userRatingCount} />
        <div className="rp-address">
          <MapPin size={12} strokeWidth={1.8} />
          <span>{place.address}</span>
        </div>
        {place.summary ? (
          <p className="rp-summary">{place.summary}</p>
        ) : (
          <p className="rp-summary rp-summary-empty">尚無景點介紹</p>
        )}
      </div>
    </div>
  )
}

export function RecommendedPlacesList({ places }: { places: RecommendedPlace[] }) {
  if (places.length === 0) {
    return <div className="empty">目前沒有推薦景點。</div>
  }
  return (
    <div className="recommended-places-list">
      {places.map((p, i) => (
        <RecommendedPlaceCard key={`${p.name}-${i}`} place={p} />
      ))}
    </div>
  )
}

// 橫向捲動版清單容器:比照 App Store/Netflix 那種橫向卡片列,復用 RecommendedPlaceCard
// 渲染單卡邏輯,只把外層排列方式從垂直堆疊改成 flex row + overflow-x: auto。
// 僅供 debug 工作台 UI 試做用,不接進正式對話畫面(見 DebugApp.tsx)。
export function RecommendedPlacesRow({ places }: { places: RecommendedPlace[] }) {
  if (places.length === 0) {
    return <div className="empty">目前沒有推薦景點。</div>
  }
  return (
    <div className="recommended-places-row">
      {places.map((p, i) => (
        <RecommendedPlaceCard key={`${p.name}-${i}`} place={p} />
      ))}
    </div>
  )
}

// ---- 假資料(僅供 UI 展示,不對應真實地點資訊) ----
// 涵蓋各種缺欄位情況:完整欄位 / 無 summary / 無評分(rating=0) / 無 photoUrl。
export const FAKE_RECOMMENDED_PLACES: RecommendedPlace[] = [
  {
    name: '清水寺',
    address: '日本〒605-0862 京都府京都市東山区清水1丁目294',
    lat: 34.9948,
    lng: 135.785,
    rating: 4.5,
    userRatingCount: 62810,
    primaryType: 'tourist_attraction',
    photoUrl: 'https://picsum.photos/seed/kiyomizu/480/320',
    summary: '京都最具代表性的古寺之一,以懸空的「清水舞台」聞名,登高可俯瞰京都市區與滿山楓紅或櫻花,是必訪景點。',
  },
  {
    name: '嵐山竹林小徑',
    address: '日本〒616-8394 京都府京都市右京区嵯峨天龍寺芒ノ馬場町',
    lat: 35.0094,
    lng: 135.6667,
    rating: 4.4,
    userRatingCount: 38210,
    primaryType: 'tourist_attraction',
    photoUrl: 'https://picsum.photos/seed/arashiyama/480/320',
    // 無 summary:後端尚未回傳介紹文字的情況
  },
  {
    name: '嵐山手作體驗工坊',
    address: '日本〒616-8385 京都府京都市右京区嵯峨天龍寺造路町',
    lat: 35.008,
    lng: 135.668,
    rating: 0, // 無評分:剛開幕或評論數不足的情況
    userRatingCount: 0,
    primaryType: 'tourist_attraction',
    photoUrl: 'https://picsum.photos/seed/workshop/480/320',
    summary: '提供和菓子與扇子手作體驗,適合親子同遊,課程約 60 分鐘,需事先預約。',
  },
  {
    name: '% Arabica 嵐山店',
    address: '日本〒616-8385 京都府京都市右京区嵯峨天龍寺芒ノ馬場町3-47',
    lat: 35.0097,
    lng: 135.6772,
    rating: 4.3,
    userRatingCount: 15420,
    primaryType: 'cafe',
    // 無 photoUrl:後端尚未接 Photo Media 的情況,前端用漸層色塊佔位
    summary: '面向桂川的人氣咖啡店,落地窗景色極佳,常需排隊,推薦外帶沿河散步享用。',
  },
  {
    name: '嵐山吉本鰻魚飯',
    address: '日本〒616-8385 京都府京都市右京区嵯峨天龍寺芒ノ馬場町',
    lat: 35.009,
    lng: 135.676,
    rating: 4.1,
    userRatingCount: 892,
    primaryType: 'restaurant',
    // 無 photoUrl 也無 summary:兩者皆缺的極端情況
  },
]
