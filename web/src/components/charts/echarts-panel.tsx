"use client";

import { useEffect, useRef } from "react";
import * as echarts from "echarts/core";
import { BarChart, LineChart, PieChart } from "echarts/charts";
import {
  GridComponent,
  LegendComponent,
  TooltipComponent,
  type GridComponentOption,
  type LegendComponentOption,
  type TooltipComponentOption,
} from "echarts/components";
import { CanvasRenderer } from "echarts/renderers";
import type { ComposeOption } from "echarts/core";
import type { BarSeriesOption, LineSeriesOption, PieSeriesOption } from "echarts/charts";
import { DetailSection } from "@/components/layout/detail-section";
import { useI18n } from "@/components/admin/i18n-provider";

echarts.use([BarChart, LineChart, PieChart, GridComponent, LegendComponent, TooltipComponent, CanvasRenderer]);

export type ChartOption = ComposeOption<
  | BarSeriesOption
  | LineSeriesOption
  | PieSeriesOption
  | GridComponentOption
  | LegendComponentOption
  | TooltipComponentOption
>;

export function EChartsPanel({ title, option, height = 260 }: { title: string; option: ChartOption; height?: number }) {
  const { locale } = useI18n();
  const ref = useRef<HTMLDivElement | null>(null);
  const chartRef = useRef<echarts.EChartsType | null>(null);

  useEffect(() => {
    if (!ref.current) return;
    const chart = echarts.init(ref.current, undefined, { renderer: "canvas" });
    chartRef.current = chart;
    const resize = () => chartRef.current?.resize();
    window.addEventListener("resize", resize);
    const resizeObserver = new ResizeObserver(resize);
    resizeObserver.observe(ref.current);
    return () => {
      window.removeEventListener("resize", resize);
      resizeObserver.disconnect();
      chart.dispose();
      chartRef.current = null;
    };
  }, []);

  useEffect(() => {
    const reduced = window.matchMedia("(prefers-reduced-motion: reduce)");
    const motion = option as Pick<LineSeriesOption, "animation" | "animationDuration" | "animationDurationUpdate">;
    const apply = () => {
      chartRef.current?.setOption({ ...option, animation: reduced.matches ? false : motion.animation ?? true, animationDuration: reduced.matches ? 0 : motion.animationDuration ?? 1000, animationDurationUpdate: reduced.matches ? 0 : motion.animationDurationUpdate ?? 500 }, { notMerge: false, lazyUpdate: true });
      if (ref.current) ref.current.dataset.chartMotion = reduced.matches ? "reduced" : "normal";
    };
    apply();
    reduced.addEventListener("change", apply);
    return () => reduced.removeEventListener("change", apply);
  }, [option]);

  return (
    <DetailSection title={title}>
      <figure className="min-w-0">
        <div ref={ref} role="img" aria-label={title} style={{ height }} className="w-full" />
        <figcaption className="text-xs leading-6 text-muted-foreground">{locale === "ja" ? "系列名は凡例、単位は軸とツールチップに表示します。値と更新時刻は下の最新メトリクス一覧でも確認できます。" : "Legend names identify series; axes and tooltips show units. Values and timestamps are also available in the latest-metrics table below."}</figcaption>
      </figure>
    </DetailSection>
  );
}
