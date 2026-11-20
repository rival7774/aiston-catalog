CREATE TABLE category (
  id integer PRIMARY KEY,
  parent_id integer REFERENCES category,
  name text NOT NULL
);
CREATE INDEX category_parent ON category(parent_id, id);
CREATE TABLE ancestry (
  ancestor integer REFERENCES category,
  descendant integer REFERENCES category,
  depth integer NOT NULL,
  PRIMARY KEY (ancestor, descendant)
);
CREATE INDEX ancestry_path ON ancestry(descendant, depth DESC);
CREATE TABLE product (
  id integer PRIMARY KEY,
  category_id integer NOT NULL REFERENCES category,
  name text NOT NULL,
  price_kopecks integer NOT NULL CHECK (price_kopecks >= 0),
  created_at timestamptz NOT NULL
);
-- Read model: a sortable entry for each product in each containing subtree.
CREATE TABLE feed (
  ancestor integer REFERENCES category,
  created_at timestamptz NOT NULL,
  product_id integer REFERENCES product,
  PRIMARY KEY (ancestor, created_at, product_id)
);
CREATE TABLE category_size (
  id integer PRIMARY KEY REFERENCES category,
  total integer NOT NULL
);
INSERT INTO category
SELECT i, CASE WHEN i<=5 THEN NULL ELSE (i-6)/4+1 END,
CASE WHEN i<=5 THEN (ARRAY['Дом','Техника','Спорт','Одежда','Досуг'])[i]
 WHEN i<=25 THEN (ARRAY['Основная коллекция','Новинки','Аксессуары','Профессиональная серия'])[(i-6)%4+1]
 WHEN i<=105 THEN (ARRAY['Классика','Минимализм','Комфорт','Премиум'])[(i-6)%4+1]
 ELSE (ARRAY['Серия Nord','Серия Terra','Серия Forma','Серия Vita'])[(i-6)%4+1] END
FROM generate_series(1,200) i;
WITH RECURSIVE paths(a,d,h) AS (
 SELECT id,id,0 FROM category UNION ALL
 SELECT p.a,c.id,p.h+1 FROM paths p JOIN category c ON c.parent_id=p.d
) INSERT INTO ancestry SELECT * FROM paths;

INSERT INTO product
SELECT i, (i-1)%200+1,
 (ARRAY['Набор','Комплект','Модель','Модуль'])[(i/200)%4+1] || ' ' ||
 (ARRAY['Nord','Terra','Forma','Vita','Linea','Mono','Aura'])[(i/800)%7+1] || ' · ' || i,
 9900+(i*773)%1500000, timestamptz '2025-01-01 00:00:00+00' + (i/32)*interval '1 minute'
FROM generate_series(1,200000) i;
INSERT INTO feed SELECT a.ancestor,p.created_at,p.id FROM product p
JOIN ancestry a ON a.descendant=p.category_id;
INSERT INTO category_size SELECT c.id,count(f.product_id) FROM category c
LEFT JOIN feed f ON f.ancestor=c.id GROUP BY c.id;
ANALYZE;
