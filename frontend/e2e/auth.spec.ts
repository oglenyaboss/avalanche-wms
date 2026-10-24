import { expect, test } from "@playwright/test";

import { loginAs } from "./helpers";

test("без входа экран ведёт на страницу входа, после входа — обратно на экран", async ({ page }) => {
  await page.goto("/assembly");
  await expect(page).toHaveURL(/\/login\?next=%2Fassembly$/);
  await page.getByLabel("Логин").fill("operator");
  await page.getByLabel("Пароль").fill("operator");
  await page.getByRole("button", { name: "Войти" }).click();
  await expect(page).toHaveURL(/\/assembly$/);
  await expect(page.getByRole("heading", { name: "Сборка" })).toBeVisible();
});

test("неверный пароль показывает ошибку", async ({ page }) => {
  await page.goto("/login");
  await page.getByLabel("Логин").fill("operator");
  await page.getByLabel("Пароль").fill("wrong");
  await page.getByRole("button", { name: "Войти" }).click();
  await expect(page.getByRole("main").getByRole("alert")).toHaveText("Неверный логин или пароль");
  await expect(page).toHaveURL(/\/login$/);
});

test("сессия переживает перезагрузку, выход ведёт на вход", async ({ page }) => {
  await loginAs(page, "operator");
  await page.reload();
  await expect(page.getByTestId("current-user")).toHaveText("operator");
  await page.getByRole("button", { name: "Выйти" }).click();
  await expect(page).toHaveURL(/\/login/);
  await page.goto("/");
  await expect(page).toHaveURL(/\/login/);
});

test("неизвестный адрес — страница 404", async ({ page }) => {
  await page.goto("/no-such-screen");
  await expect(page.getByRole("heading", { name: "Страница не найдена" })).toBeVisible();
});
