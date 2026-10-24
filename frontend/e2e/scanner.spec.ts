import { expect, test, type Page } from "@playwright/test";

import { loginAs } from "./helpers";

const scanField = (page: Page) => page.getByRole("textbox", { name: "Код", exact: true });
const latestScans = (page: Page) => page.getByRole("list", { name: "Последние сканы" }).getByRole("listitem");
const firstScan = (page: Page) => latestScans(page).first();

async function openEmulator(page: Page, path = "/scanner-check") {
  await loginAs(page, "operator");
  await page.goto(path);
  await page.getByRole("button", { name: "Эмулятор сканера" }).click();
  await expect(page.getByRole("complementary", { name: "Эмулятор сканера" })).toBeVisible();
}

test("эмулятор сканирует в активное поле и оставляет фокус в нём", async ({ page }) => {
  await openEmulator(page);
  await expect(page.getByText("Сканирует в: Код")).toBeVisible();
  await page.getByRole("button", { name: "Буфер BUF-01" }).click();
  await expect(firstScan(page)).toContainText("66666666-6666-6666-6666-666666666666");
  await expect(firstScan(page)).toContainText("эмулятор");
  await expect(scanField(page)).toBeFocused();
  await expect(scanField(page)).toBeEmpty();
});

test("повторный скан того же кода обрабатывается дважды", async ({ page }) => {
  await openEmulator(page);
  const buffer = page.getByRole("button", { name: "Буфер BUF-01" });
  await buffer.click();
  await buffer.click();
  await expect(latestScans(page)).toHaveCount(2);
  await expect(firstScan(page)).toContainText("66666666-6666-6666-6666-666666666666");
});

test("сканер-клавиатура: Enter в поле скана фиксирует источник «сканер»", async ({ page }) => {
  await openEmulator(page);
  const code = scanField(page);
  await code.fill("QR-0001");
  await code.press("Enter");
  await expect(firstScan(page)).toContainText("QR-0001");
  await expect(firstScan(page)).toContainText("сканер");
  await expect(code).toBeFocused();
});

test("ручной ввод в эмуляторе сканирует как «эмулятор»", async ({ page }) => {
  await openEmulator(page);
  await page.getByRole("textbox", { name: "Код для скана" }).fill("ABC-1");
  await page.getByRole("button", { name: "Сканировать", exact: true }).click();
  await expect(firstScan(page)).toContainText("ABC-1");
  await expect(firstScan(page)).toContainText("эмулятор");
});

test("поиск кода фильтрует список эмулятора", async ({ page }) => {
  await openEmulator(page);
  await page.getByRole("textbox", { name: "Поиск кода" }).fill("lenovo");
  await expect(page.getByRole("button", { name: "Ноутбук Lenovo X1" })).toBeVisible();
  await expect(page.getByRole("button", { name: "Буфер BUF-01" })).toHaveCount(0);
});

test("без активного поля скана эмулятор выключен", async ({ page }) => {
  await loginAs(page, "operator");
  await page.goto("/");
  await page.getByRole("button", { name: "Эмулятор сканера" }).click();
  await expect(page.getByText("Нет активного поля скана")).toBeVisible();
  await expect(page.getByRole("button", { name: "Буфер BUF-01" })).toBeDisabled();
});
