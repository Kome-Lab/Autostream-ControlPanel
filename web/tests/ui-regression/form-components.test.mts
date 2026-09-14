import "./component-loader.mts";
import assert from "node:assert/strict";
import { createElement } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import test from "node:test";

const { Field } = await import("../../src/components/forms/field.tsx");
const { DetailSection, SectionNavigation } = await import("../../src/components/layout/detail-section.tsx");

test("UI-FORM-001: actual field keeps its control id and links description, validation and disabled reason", () => {
  const fieldProps = {
    label: "Name", description: "Display name", error: "Required", disabledReason: "Saving",
    children: createElement((props: {id: string; disabled: boolean; "aria-describedby": string}) => createElement("input", props), { id: "name", disabled: true, "aria-describedby": "existing" }),
  };
  const html = renderToStaticMarkup(createElement(Field, fieldProps));
  assert.match(html, /for="name"/);
  assert.match(html, /aria-describedby="existing name-description name-error name-disabled"/);
  assert.match(html, /aria-invalid="true"/);
  for (const id of ["name-description", "name-error", "name-disabled"]) assert.match(html, new RegExp(`id="${id}"`));
  assert.equal((html.match(/<input\b/g) || []).length, 1);
});

test("UI-DETAIL-001: section navigation does not overwrite an existing create/detail URL fragment", () => {
  const sectionProps = { id: "overview", title: "Overview", children: createElement("button", null, "Action") };
  const html = renderToStaticMarkup(createElement("div", null,
    createElement(SectionNavigation, { label: "Sections", items: [{ id: "overview", label: "Overview" }] }),
    createElement(DetailSection, sectionProps),
  ));
  assert.doesNotMatch(html, /href=/);
  assert.match(html, /aria-label="Sections"/);
  assert.match(html, /<h2 id=/);
  assert.equal((html.match(/>Action<\/button>/g) || []).length, 1);
});
