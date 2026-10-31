# Tacet 前端组件与模块边界

本轮将原型移植引擎替换为 React 组件。原先 `App.tsx` 只提供窗口节点，`engine/*` 拼接 HTML 字符串、覆盖 `innerHTML`，并依赖 `data-a` 全局事件表、可任意修改的 `S:any` 和 `@ts-nocheck`。这种实现虽然按原型章节拆成了文件，页面生命周期与状态更新仍由同一个命令式引擎控制，无法通过 props 组合或复用 React 组件。

现在所有页面、菜单、气泡、对话框、抽屉和提示均由 JSX 渲染；业务字符串按文本节点输出。唯一的 `dangerouslySetInnerHTML` 在 `ui/Icon.tsx`，来源是固定的本地图标路径表，不能传入文档内容。

## 目录与依赖

| 位置 | 职责 | 主要依赖 |
| --- | --- | --- |
| `domain/types.ts` | 文档、命中、模型、方案、规则、批次、复核、历史等数据类型 | 无 |
| `domain/{single,batch,review,presets}.ts` | 数据计算和状态转移；接收对象并返回结果或修改调用方提供的 draft | 领域类型、格式化与确定性随机数；不访问 DOM、store 或服务 |
| `store/` | 应用状态、订阅与派生选择器 | Immer、React `useSyncExternalStore`、domain |
| `actions/` | 用户操作、导航、等待操作和模拟任务计时器的编排 | store、domain、overlay、宿主 services |
| `ui/` | 无具体任务知识的视觉组件 | React、本地图标与品牌资源 |
| `overlay/` | 临时弹层状态和公共弹层容器 | store 工具、ui |
| `features/` | 按功能组织的页面与业务组件 | domain、store、actions、ui、overlay |
| `services/` | 浏览器和 Wails 宿主适配 | `TacetService`、生成的 Wails bindings |
| `dev/` | 场景导航、深链接和场景夹具 | actions、store；仅开发/测试或显式 scenes 构建加载 |
| `styles/` | 全局令牌、控件与各功能样式 | `index.css` 按固定层叠顺序导入；宿主样式最后覆盖 |

`App` 只组合 `TitleBar`、`Rail`、`MainView` 和 `OverlayHost`，负责宿主订阅及主题属性。普通点击直接使用 React 回调；文档键盘与滚动监听随对应组件挂载/卸载，不再通过全局 action 字符串查表。

## 组件复用落点

| 可复用组件 | 使用位置与目的 |
| --- | --- |
| `Button` / `IconButton` / `LinkButton` / `SelectButton` | 统一按钮变体、禁用态、图标与菜单入口 |
| `Segmented` / `Switch` / `Tabs` / `FilterChips` | 方案、预检、设置、文档视图和表格筛选共用选择控件 |
| `List` / `Row` / `SectionTitle` / `Page` | 设置、方案和预检共用行布局与页面结构 |
| `Field` / `TextArea` / `SearchBox` / `KeyValue` | 统一表单容器与详情排列；表单草稿由所在对话框保存 |
| `Pill` / `Tag` / `Swatch` / `FileIcon` | 统一状态、版本、类别和格式标识 |
| `Steps` / `SegmentBar` / `ResultCard` / `StatList` | 单份进度、批次进度与复核导出共享展示 |
| `ModelCard` | 首页等待、预检、全局等待提示和插件管理共用模型状态与操作 |
| `HistoryRows` | 首页最近三条与全部历史对话框共用同一行组件 |
| `ManifestPage` | 单文件、批次、历史文件、历史批次共用清单与分页 |
| `ConfigHeader` / `ItemList` | 方案与规则集共用主从列表、版本与复制入口 |
| `Modal` / `Popover` / `Drawer` / `MenuList` / `Toast` | 公共容器控制定位、关闭和焦点；内容由功能组件提供 |

单文件复核进一步拆为 `PageThumbnails`、`DocumentPane`、`ReviewSidebar`，文档内部由 `DocumentView`、`Paper`、`HitSpan`、`ImageBlock` 组合。批次复核拆为 `ReviewQueue`、`ReviewDocument`、`ReviewPaper`、`ReviewPanel`、`ReviewDone`。组件边界按职责划分，避免把整个页面藏进一个字符串适配组件。

## 状态与生命周期

- 业务数据保存在 `AppState`；Immer 更新创建新快照，保留未改变分支的引用。`useAppState(selector)` 订阅所需数据；返回新数组/对象的选择器必须缓存或提供比较函数。
- 弹层使用独立的 `overlayStore`：互斥对话框/抽屉、一个气泡/菜单、一个 toast。业务状态不会携带 HTML 或 React 元素。等待模型的 continuation 单独保存，避免将回调写进业务数据。
- 规则编辑、导入名称和插件安装步骤等短期草稿使用组件自身 state。取消弹窗即丢弃；不会为了一个输入字段更新全站业务状态。
- 检测、导出、模型下载与批次时钟目前仍是演示计时器，统一由 `actions/timers.ts` 管理。停止/替换任务会清理句柄；单份任务使用 `runId` 防止旧回调更新后来的任务。全终态批次停止时钟，仍有搁置文件的批次可继续复核。
- 撤销必须保存修改前的值，不能保留正在被修改的 Immer draft。单份与批次复核都以数据快照恢复，已导出文件的撤销仍受原有规则限制。
- 焦点、选区、滚动和气泡锚点属于视图职责，允许局部读取 DOM；不允许靠 DOM 写入表达业务状态。对比视图保留页面位置，组件卸载清理监听与延迟任务。
- 文件表每次最多追加 60 行，清单每次追加 80 行。当前数据源仍是前端示例集合；这不代表已经实现后端的大规模分页查询。

## 验证与开发

```sh
# 在仓库根目录生成宿主绑定；需要已安装 Wails 3 CLI
wails3 generate bindings -clean=true -ts -i ./cmd/tacet-desktop

cd frontend
npm ci
npm run typecheck
npm test
npm run build

# 场景和浏览器验收（单独终端运行 preview）
npm run build:scenes
npm run preview -- --host 127.0.0.1 --port 4173
npm run compare
npm run walkthrough
```

`WALKTHROUGH_OUT=/tmp/tacet-react-walkthrough npm run walkthrough` 可将本轮报告与截图写在临时目录，避免覆盖已有走查资产。走查脚本通过可访问名称和组件结构查找控件，不再依赖已删除的 `data-a`。

测试分两层：`domain/__tests__` / `store/__tests__` 覆盖状态转移、不可变更新与快照，`test/` 通过实际渲染后的界面操作验证各功能。每例重置应用、弹层、等待操作和计时器，避免测试之间继承状态。

## 当前能力边界

本轮完成的是前端组件化和模块化。`TacetService` 提供应用信息、最小化、退出及关闭请求订阅；下载、插件安装、检测、导出、更新与工作区清理仍是前端演示。真正的命令/查询/事件接口、有界后端查询、原生文件句柄令牌及磁盘输出，应在后续服务接入中实现；组件和领域层不应直接导入 Wails bindings。
