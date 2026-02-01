-- создание пользователя debezium
DO $$
BEGIN
    IF NOT EXISTS (SELECT FROM pg_catalog.pg_roles WHERE rolname = 'debezium') THEN
        CREATE USER debezium WITH REPLICATION LOGIN PASSWORD 'debezium';
    END IF;
END
$$;


-- привилегии для чтения таблиц пользователю debezium
DO $$
BEGIN
    IF EXISTS (SELECT FROM pg_database WHERE datname = 'WMS') THEN
        EXECUTE 'GRANT ALL PRIVILEGES ON DATABASE "WMS" TO debezium';
    END IF;
END
$$;

-- подключение расширения для управления бд (подключения к ней, так как подключение идет по имени пользователя, а нужно по данной бд)
CREATE EXTENSION IF NOT EXISTS dblink;

-- инициализация бд WMS (создание таблицы)
DO $$
DECLARE
    conn_result TEXT;
    sql_cmd TEXT;
BEGIN
    IF EXISTS (SELECT FROM pg_database WHERE datname = 'WMS') THEN
        BEGIN
            PERFORM dblink_connect('conn_wms', 'dbname=WMS user=postgres password=1234');
            PERFORM dblink_exec('conn_wms', '
                CREATE TABLE IF NOT EXISTS orders (
                    id SERIAL PRIMARY KEY,
                    order_number VARCHAR(50) NOT NULL UNIQUE,
                    customer_name VARCHAR(100) NOT NULL,
                    product_name VARCHAR(100) NOT NULL,
                    quantity INTEGER NOT NULL DEFAULT 1,
                    price DECIMAL(10, 2) NOT NULL,
                    status VARCHAR(20) DEFAULT ''pending'',
                    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
                    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
                )
            ');
            
            PERFORM dblink_exec('conn_wms', 'ALTER TABLE orders REPLICA IDENTITY FULL');

            PERFORM dblink_exec('conn_wms', 'DROP PUBLICATION IF EXISTS debezium_pub');
            PERFORM dblink_exec('conn_wms', 'CREATE PUBLICATION debezium_pub FOR TABLE orders');
            
            PERFORM dblink_exec('conn_wms', 'GRANT SELECT ON orders TO debezium');
            PERFORM dblink_exec('conn_wms', 'GRANT SELECT ON orders_id_seq TO debezium');
            
            PERFORM dblink_exec('conn_wms', 
                'INSERT INTO orders (order_number, customer_name, product_name, quantity, price, status) ' ||
                'VALUES ' ||
                '(''ORD001'', ''John Doe'', ''Laptop'', 1, 999.99, ''completed''), ' ||
                '(''ORD002'', ''Jane Smith'', ''Mouse'', 2, 49.99, ''shipped''), ' ||
                '(''ORD003'', ''Bob Johnson'', ''Keyboard'', 1, 89.99, ''pending'') ' ||
                'ON CONFLICT (order_number) DO NOTHING'
            );
            
            PERFORM dblink_exec('conn_wms', 'DROP TRIGGER IF EXISTS update_orders_updated_at ON orders');
            PERFORM dblink_exec('conn_wms', 'DROP FUNCTION IF EXISTS update_updated_at_column()');
            
            sql_cmd := '
                CREATE FUNCTION update_updated_at_column()
                RETURNS TRIGGER AS $func$
                BEGIN
                    NEW.updated_at = CURRENT_TIMESTAMP;
                    RETURN NEW;
                END;
                $func$ LANGUAGE plpgsql
            ';
            PERFORM dblink_exec('conn_wms', sql_cmd);
            
            PERFORM dblink_exec('conn_wms', 
                'CREATE TRIGGER update_orders_updated_at ' ||
                'BEFORE UPDATE ON orders ' ||
                'FOR EACH ROW ' ||
                'EXECUTE FUNCTION update_updated_at_column()'
            );
            
            PERFORM dblink_disconnect('conn_wms');
            
            RAISE NOTICE 'База данных WMS успешно инициализирована';
            
        EXCEPTION WHEN OTHERS THEN
            RAISE NOTICE 'Не удалось выполнить инициализацию в базе WMS: %', SQLERRM;
            BEGIN
                PERFORM dblink_disconnect('conn_wms');
            EXCEPTION WHEN OTHERS THEN
            END;
        END;
    ELSE
        RAISE NOTICE 'База данных WMS не существует, возможно она будет создана позже';
    END IF;
END
$$;


-- создание слота для реплекация
DO $$
BEGIN
    IF NOT EXISTS (
        SELECT FROM pg_replication_slots WHERE slot_name = 'debezium_slot'
    ) AND EXISTS (SELECT FROM pg_database WHERE datname = 'WMS') THEN
        BEGIN
            PERFORM dblink_connect('conn_slot', 'dbname=WMS user=postgres password=1234');
            PERFORM dblink_exec('conn_slot', 
                'SELECT pg_create_logical_replication_slot(''debezium_slot'', ''pgoutput'')'
            );
            PERFORM dblink_disconnect('conn_slot');
            RAISE NOTICE 'Слот репликации debezium_slot создан';
        EXCEPTION WHEN OTHERS THEN
            RAISE NOTICE 'Не удалось создать слот репликации: %', SQLERRM;
        END;
    END IF;
END
$$;