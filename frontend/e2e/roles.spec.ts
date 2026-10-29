import { expect, test } from "@playwright/test";

import { loginAs } from "./helpers";

const menu = {
  operator: ["Приёмка у ворот", "Стол приёмки", "Раскладка", "Сборка", "Отгрузка", "Трассировка товара", "Аналитика", "Мой ключ"],
  admin: ["Трассировка товара", "Аналитика", "Аудит", "Мой ключ", "Ключи организации"],
  auditor: ["Аудит"],
  customer: [],
} as const;

const allItems = [...new Set(Object.values(menu).flat())];

for (const [username, items] of Object.entries(menu)) {
  test(`${username}: на главной только разделы своей роли`, async ({ page }) => {
    await loginAs(page, username);
    const main = page.getByRole("main");
    for (const title of allItems) {
      const link = main.getByRole("link", { name: title, exact: true });
      if ((items as readonly string[]).includes(title)) await expect(link).toBeVisible();
      else await expect(link).toHaveCount(0);
    }
    if (items.length === 0) await expect(main.getByText("Для вашей роли нет доступных разделов.")).toBeVisible();
  });
}

test("экран чужой роли показывает «Нет доступа»", async ({ page }) => {
  await loginAs(page, "admin");
  await page.goto("/assembly");
  await expect(page.getByRole("heading", { name: "Нет доступа" })).toBeVisible();
});

test("меню ведёт на экран", async ({ page, isMobile }) => {
  await loginAs(page, "operator");
  if (isMobile) await page.getByRole("button", { name: "Открыть меню" }).click();
  await page.getByRole("navigation", { name: "Разделы" }).getByRole("link", { name: "Раскладка" }).click();
  await expect(page).toHaveURL(/\/putaway$/);
  await expect(page.getByRole("heading", { name: "Раскладка" })).toBeVisible();
  await expect(page.getByRole("navigation", { name: "Разделы" })).toHaveCount(isMobile ? 0 : 1);
});
