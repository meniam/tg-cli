# Панель списка CardMenubar

Строка над таблицей в карточке: поиск, фильтры, действия со списком,
разворот. Сами фильтры — в `filters.md`, таблица — в `tables.md`.

## Где стоит

- `AgGrid` лежит в `Card`, сверху `CardMenubar`, даже если строк немного
  (журнал выгрузки). `CardHeader` с заголовком над гридом не ставим: имя
  таблицы — `ariaLabel` грида.
- У грида с панелью своего тулбара нет: пропы `search`, `actions` и кнопку
  Excel самого `AgGrid` не используем. Вторая строка тулбара под панелью —
  баг.

## Блоки

Слева направо:

```
[поиск] [быстрые фильтры] [Фильтры]  ……пусто……  [быстрые действия] [Действия ⌄]
```

- **Поиск** — `InputSearch` без своих классов высоты и ширины: размер в
  панели задаёт кит (`h-8 w-64`, уже `md` поле тянется по месту).
  Подсказка влезает в поле: «Номер, партнёр, товар», а не «Поиск по номеру
  заказа, партнёру или товару». Полный перечень полей, если он нужен, —
  в `aria-label`. У серверного списка поиск уходит запросом по Enter и при
  уходе из поля, а не на каждую букву: `InputSearch onApply`, своей формы с
  черновиком не пишем.
- **Быстрые фильтры** — самые важные, в идеале 3–4, каждый в своём
  `MenubarMenu` с `MenubarFilterTrigger` (`filters.md`). Их может не быть
  вовсе.
- **«Фильтры»** — кнопка `FilterSidebarTrigger mode="menubar"` с остальными
  полями (`filters.md`).
- **Быстрые действия** — видимые кнопки сверхважных действий. Агент их сам
  не ставит, только по прямому указанию пользователя.
- **«Действия»** — `MenubarActionsTrigger`: кнопка «Действия ⌄» видом как
  `Select`, а не «⋯». Всё, что агент добавляет в панель сам, — пунктами
  этого меню.

## Меню «Действия»

Порядок пунктов, если пользователь прямо не попросил другой:

1. «Развернуть на весь экран» — `CardExpandMenubarItem`.
2. За разделителем — действия со списком («Добавить заказ», «Обновить») и
   выгрузка.
3. Последним — «Сбросить фильтры»:
   `<FilterResetMenubarItem active={hasFilters} onReset={reset} />`. Пресет
   сам ставит разделитель перед собой; руками `MenubarItem` с `FilterX` не
   собираем.

- Выгрузку в Excel ставим, только если её попросили: пункт
  `AgGridExcelMenubarItem`, у грида `exportName` и `exportButton={false}`.
  Не просили — ни пункта, ни `exportName`.
- Отдельных кнопок Excel и CSV в панели нет.
- Условные пункты ставим без проверок на разделители: `MenubarContent` сам
  прячет лишний разделитель в начале, в конце и подряд.

```tsx
<Card fill expandable expandKey="orders">
  <CardMenubar>
    <InputSearch value={table.q} onChange={…} placeholder="Номер, партнёр, товар" />
    <MenubarMenu>
      <MenubarFilterTrigger icon={CircleCheck} label={statusLabel ?? 'Статус'} active={status !== null} />
      <MenubarContent>…</MenubarContent>
    </MenubarMenu>
    <MenubarMenu>
      <MenubarActionsTrigger />
      <MenubarContent align="end">
        <CardExpandMenubarItem />
        <MenubarSeparator />
        <MenubarItem onSelect={openNew}><Icon icon={Plus} />Добавить заказ</MenubarItem>
        <FilterResetMenubarItem active={hasFilters} onReset={reset} />
      </MenubarContent>
    </MenubarMenu>
  </CardMenubar>
  <CardTable fill><AgGrid fill ariaLabel="Заказы" … /></CardTable>
</Card>
```

## Разворот

- Развёрнутая карточка занимает экран от края до края. «Свернуть» — видимая
  кнопка справа от «Действий», её ставит сам `CardMenubar`: выход из
  развёрнутого вида не прячем в меню.
- Своей кнопки разворота грид в карточке с панелью не рисует:
  `cardExpandButton` по умолчанию выключен. Грид без панели включает его
  явно; `cardExpandButton={false}` руками не пишем.
- Разворот живёт в адресе по `expandKey` карточки (`?orders.expanded=true`),
  поэтому ключ латиницей и уникален на странице.

## Checklist

1. Грид лежит в `Card` с `CardMenubar`, `CardHeader` над гридом нет, имя —
   `ariaLabel`.
2. У грида нет своего тулбара: `search`, `actions` и кнопки Excel не заданы.
3. Блоки идут слева направо: поиск, быстрые фильтры, «Фильтры», быстрые
   действия, «Действия».
4. У `InputSearch` нет своих классов размера, подсказка влезает в поле.
5. Видимые быстрые действия стоят только по указанию пользователя.
6. «Действия» — `MenubarActionsTrigger`, а не «⋯».
7. В «Действиях» первым «Развернуть», последним `FilterResetMenubarItem`.
8. Excel — только по просьбе, пунктом `AgGridExcelMenubarItem`.
9. «Свернуть» руками не добавлен, `cardExpandButton={false}` не написан.
10. У разворачиваемой карточки есть `expandKey` латиницей.
