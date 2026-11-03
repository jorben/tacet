import { afterEach, describe, expect, it, vi } from "vitest";
import { act, cleanup, render } from "@testing-library/react";
import { loadConfig, stopConfigSync } from "../actions/liveConfig";
import { setCategoryLabels } from "../actions/modelMapping";
import * as overlays from "../overlay/store";
import { RulesEditor } from "../features/presets/RulesEditor";
import { ModelMappingEditor } from "../features/presets/ModelMappingEditor";
import { getState, store } from "../store";
import { setService } from "../services";
import { mockService } from "../services/mock";
import type { EngineCategory } from "../services/types";
import { FakeEngine } from "./liveFake";

afterEach(() => { cleanup(); stopConfigSync(); setService(null); });

describe("产品澄清 · 类别目录加载状态", () => {
  it.each(["规则页", "模型页"] as const)("%s：加载中显示原始 id，不标未登记；成功后按目录显示名称并判定", async (page) => {
    await checkCatalog(page, "ready");
  });
  it.each(["规则页", "模型页"] as const)("%s：加载中不标未登记；失败后保留原目录并恢复登记判定", async (page) => {
    await checkCatalog(page, "failed");
  });
});

describe("产品澄清 · 移动标签提示", () => {
  it.each([
    { labels: ["private_date"], text: "已将 private_date 从 日期 移到 公司名" },
    { labels: ["private_date", "phone", "email", "private_address", "account"], text: "已将 account 从 账号 移到 公司名；已将 email 从 邮箱 移到 公司名；已将 phone 从 电话 移到 公司名；等 2 个标签" },
  ])("移动 $labels：只弹一条 toast，带原始标签名和实体显示名，最多列三处", async ({ labels, text }) => {
    store.update((d) => { d.presets = []; });
    const toast = vi.spyOn(overlays, "toast");
    try {
      await setCategoryLabels("astr", "organization", labels);
      expect(toast).toHaveBeenCalledTimes(1);
      expect(toast).toHaveBeenCalledWith(text);
      expect(overlays.overlayStore.get().toast?.msg).toBe(text);
    } finally { toast.mockRestore(); }
  });
});

async function checkCatalog(page: "规则页" | "模型页", outcome: "ready" | "failed") {
  const fake = new FakeEngine();
  let complete!: (list: EngineCategory[] | null) => void;
  const response = new Promise<EngineCategory[] | null>((resolve) => { complete = resolve; });
  fake.categories = async () => {
    const list = await response;
    if (!list) throw new Error("catalog unavailable");
    return list;
  };
  setService({ ...mockService, engine: fake });
  store.update((d) => {
    d.rulesets = [{ id: "custom", name: "测试集", ver: "v1", rules: ["phone", "customer_id", "ghost"].map((cat) => ({ name: cat, cat, on: true, kind: "词表", body: "甲" })) }];
    d.rulesetSel = "custom";
    d.models[0]!.mapping = { map: { ...d.models[0]!.labels, private_person: "customer_id", private_address: "ghost" } };
  });
  const fallback = getState().categories;
  render(page === "规则页" ? <RulesEditor /> : <ModelMappingEditor />);
  const row = (id: string) => document.querySelector(`[data-category="${id}"]`)!;
  let request!: Promise<void>;
  act(() => { request = loadConfig(); });
  try {
    expect(getState().categoryStatus).toBe("loading");
    for (const id of ["phone", "customer_id", "ghost"]) {
      expect(row(id).querySelector(".entity-name")).toHaveTextContent(id);
      expect(row(id)).not.toHaveTextContent("未登记实体");
    }
  } finally {
    await act(async () => {
      complete(outcome === "ready" ? [...fake.categoryList, { id: "customer_id", name: "客户编号", placeholder: "[客户编号]", origin: "custom", manual: true }] : null);
      await request;
    });
  }
  expect(getState().categoryStatus).toBe(outcome);
  expect(row("phone").querySelector(".entity-name")).toHaveTextContent("电话");
  expect(row("ghost")).toHaveTextContent("ghost未登记实体");
  if (outcome === "ready") {
    expect(row("customer_id").querySelector(".entity-name")).toHaveTextContent("客户编号");
    expect(row("customer_id")).not.toHaveTextContent("未登记实体");
  } else {
    expect(getState().categories).toBe(fallback);
    expect(row("customer_id")).toHaveTextContent("customer_id未登记实体");
  }
}
