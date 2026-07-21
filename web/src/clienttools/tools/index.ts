import type { ClientTool } from '../ClientToolsBridge'
import { tripEntryAdd } from './tripEntryAdd'
import { tripEntryDelete } from './tripEntryDelete'
import { tripEntryUpdate } from './tripEntryUpdate'
import { tripEntryList } from './tripEntryList'
import { tripListBatches } from './tripListBatches'

// defaultClientTools — 目前這個 POC 頁面要用到的全部前端工具,一次匯總成
// 陣列給 ClientToolsBridge 的建構子。未來新增一個工具,不需要碰
// ClientToolsBridge.ts:新建一個 tools/xxx.ts 檔案 export 一個 ClientTool
// 常數,再到這裡加一行 import + 加進陣列即可。
export const defaultClientTools: ClientTool[] = [
  tripEntryAdd,
  tripEntryDelete,
  tripEntryUpdate,
  tripEntryList,
  tripListBatches,
]
