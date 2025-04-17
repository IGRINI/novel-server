document.addEventListener('DOMContentLoaded', () => {

    const translations = {
        en: {
            nav_features: 'Features',
            nav_how_it_works: 'How it Works',
            nav_download: 'Download',
            hero_title: 'Create the Universe. Write the Destiny.',
            hero_tagline: 'TaleShift: Stories born from your imagination and <strong>brought to life by AI</strong>.',
            hero_description: 'Here, you are the author and the hero. Describe your world, and the AI will weave a <strong>unique story</strong> for you with unpredictable consequences. <strong>Every decision changes everything</strong>.',
            cta_app_store: 'App Store',
            cta_google_play: 'Google Play',
            how_title: 'From Idea to Living World',
            how_description: 'Describe your concept - setting, hero, atmosphere. The AI generates a <strong>unique story</strong> for you, full of secrets and hidden variables. You control the plot using cards: each decision (swipe left or right) changes the world and leads to an <strong>unpredictable ending</strong>.',
            how_step1_title: '1. Your Idea',
            how_step1_text: 'Describe setting, hero, atmosphere.',
            how_step2_title: '2. AI Magic',
            how_step2_text: '<strong>AI creates</strong> a unique world and plot.',
            how_step3_title: '3. Make Choices',
            how_step3_text: 'Control the story via <strong>card swipes</strong>.',
            how_step4_title: '4. Reap Results',
            how_step4_text: 'Witness <strong>unpredictable consequences</strong>.',
            features_title: 'Why TaleShift?',
            feature1_title: 'Limitless Worlds',
            feature1_text: 'Your imagination is the only limit. From cyberpunk megacities to fantasy kingdoms - <strong>create and explore</strong> universes that never existed before.',
            feature2_title: 'Choices with Depth',
            feature2_text: '<strong>Every decision matters</strong>. It affects hidden parameters, relationships, and can lead to <strong>unexpected consequences</strong> dozens of scenes later.',
            feature3_title: 'Living Stories',
            feature3_text: "Worlds react to your actions. Share your creations, play through others' stories, and watch how the narrative can <strong>break or... look back at you</strong>.",
            testimonials_title: 'What Players Say',
            testimonial1_quote: '"Mind-blowing! I started with a simple idea, and it turned into an epic saga I couldn\'t predict. <strong>Every choice felt impactful</strong>."',
            testimonial1_author: '- Alex_Stormrider',
            testimonial2_quote: '"It\'s like dreaming while awake. The AI weaves such intricate details... Sometimes it feels like <strong>the story *knows* me</strong>."',
            testimonial2_author: '- Seraphina_Reads',
            testimonial3_quote: '"Finally, a game where <strong>my creativity matters</strong>! Sharing my world and seeing others explore it is incredibly rewarding."',
            testimonial3_author: '- GlitchWizard',
            audience_title: 'Is TaleShift For You?',
            audience_text: 'TaleShift is perfect for <strong>creative minds</strong>, lovers of <strong>interactive fiction and RPGs</strong>, writers looking for inspiration, and anyone seeking a truly <strong>unique narrative experience</strong> where their choices shape the universe.',
            audience_item1: 'Interactive Story Fans',
            audience_item2: 'Role-Playing Gamers',
            audience_item3: 'Creative Writers & Worldbuilders',
            audience_item4: 'Players Seeking Unique Experiences',
            gallery_title: 'Take a Look at TaleShift',
            gallery_placeholder: '(App screenshots gallery will be here)',
            cta_final_title: 'Ready to Start Your Story?',
            cta_final_description: 'Download TaleShift and dive into worlds waiting for their creator.',
            cta_app_store_final: 'App Store',
            cta_google_play_final: 'Google Play',
            footer_copy: '© 2025 TaleShift. All rights reserved.',
            footer_privacy: 'Privacy Policy',
            footer_terms: 'Terms of Use',
            cookie_text: 'We use cookies and analytics tools to improve our site and your experience. By continuing to use the site, you agree to this.',
            cookie_learn_more: 'Learn more in our',
            cookie_privacy_link: 'Privacy Policy',
            cookie_accept: 'Got it!',
        },
        ru: {
            nav_features: 'Особенности',
            nav_how_it_works: 'Как это работает',
            nav_download: 'Скачать',
            hero_title: 'Создай Вселенную. Напиши Судьбу.',
            hero_tagline: 'TaleShift: Истории, рождённые твоим воображением и <strong>оживлённые ИИ</strong>.',
            hero_description: 'Здесь ты — автор и герой. Опиши свой мир, и ИИ сплетёт для тебя <strong>уникальную историю</strong> с непредсказуемыми последствиями. <strong>Каждое решение меняет всё</strong>.',
            cta_app_store: 'App Store',
            cta_google_play: 'Google Play',
            how_title: 'От Идеи до Живого Мира',
            how_description: 'Опиши свою задумку — сеттинг, герой, атмосфера. Искусственный интеллект генерирует для тебя <strong>уникальную историю</strong>, полную тайн и скрытых переменных. Ты управляешь сюжетом с помощью карт: каждое решение (свайп влево или вправо) меняет мир и ведет к <strong>непредсказуемому финалу</strong>.',
            how_step1_title: '1. Твоя Идея',
            how_step1_text: 'Опиши сеттинг, героя, атмосферу.',
            how_step2_title: '2. Магия ИИ',
            how_step2_text: '<strong>ИИ создает</strong> уникальный мир и сюжет.',
            how_step3_title: '3. Делай Выбор',
            how_step3_text: 'Управляй сюжетом через <strong>свайпы карт</strong>.',
            how_step4_title: '4. Пожинай Плоды',
            how_step4_text: 'Наблюдай за <strong>непредсказуемыми последствиями</strong>.',
            features_title: 'Почему TaleShift?',
            feature1_title: 'Безграничные Миры',
            feature1_text: 'Твоя фантазия — единственный предел. От киберпанк-мегаполисов до фэнтезийных королевств — <strong>создавай и исследуй</strong> вселенные, которых ещё не существовало.',
            feature2_title: 'Выборы с Глубиной',
            feature2_text: '<strong>Каждое решение имеет значение</strong>. Оно влияет на скрытые параметры, отношения и может привести к <strong>неожиданным последствиям</strong> спустя десятки сцен.',
            feature3_title: 'Живые Истории',
            feature3_text: 'Миры реагируют на твои действия. Делись творениями, проходи истории других и наблюдай, как повествование может <strong>ломаться или... смотреть на тебя в ответ</strong>.',
            testimonials_title: 'Что Говорят Игроки',
            testimonial1_quote: '"Удивительно! Я начал с простого идеи, и она превратилась в эпическое повествование, которое я не смог предсказать. <strong>Каждый выбор чувствовался важным</strong>."',
            testimonial1_author: '- Alex_Stormrider',
            testimonial2_quote: '"Это как мечтать наяву. ИИ вплетает такие сложные детали... Иногда кажется, что <strong>история *знает* меня</strong>."',
            testimonial2_author: '- Seraphina_Reads',
            testimonial3_quote: '"Наконец-то игра, где <strong>моё творчество имеет значение</strong>! Делиться своим миром и видеть, как другие его исследуют, — это невероятно приятно."',
            testimonial3_author: '- GlitchWizard',
            audience_title: 'Для Тебя TaleShift?',
            audience_text: 'TaleShift идеально подходит для <strong>творческих умов</strong>, любителей <strong>интерактивной литературы и RPG</strong>, писателей в поисках вдохновения и всех, кто ищет по-настоящему <strong>уникальный нарративный опыт</strong>, где их выборы формируют вселенную.',
            audience_item1: 'Любители интерактивных историй',
            audience_item2: 'Игроки в ролевые игры',
            audience_item3: 'Творческие писатели и строители миров',
            audience_item4: 'Игроки, ищущие уникальный опыт',
            gallery_title: 'Взгляни на TaleShift',
            gallery_placeholder: '(Здесь будет галерея скриншотов приложения)',
            cta_final_title: 'Готов Начать Свою Историю?',
            cta_final_description: 'Скачай TaleShift и погрузись в миры, которые ждут своего создателя.',
            cta_app_store_final: 'App Store',
            cta_google_play_final: 'Google Play',
            footer_copy: '© 2025 TaleShift. Все права защищены.',
            footer_privacy: 'Политика Конфиденциальности',
            footer_terms: 'Условия Использования',
            cookie_text: 'Мы используем файлы cookie и инструменты аналитики для улучшения нашего сайта и вашего опыта. Продолжая использовать сайт, вы соглашаетесь с этим.',
            cookie_learn_more: 'Узнайте больше в нашей',
            cookie_privacy_link: 'Политике Конфиденциальности',
            cookie_accept: 'Понятно!',
        }
    };

    const langButtons = document.querySelectorAll('.lang-button');
    const translatableElements = document.querySelectorAll('[data-lang-key]');

    function setLanguage(lang) {
        if (!translations[lang]) {
            console.warn(`Language '${lang}' not supported. Falling back to 'en'.`);
            lang = 'en'; // Fallback to English if lang is invalid
        }

        translatableElements.forEach(element => {
            const key = element.getAttribute('data-lang-key');
            if (translations[lang] && translations[lang][key]) {
                // Возвращаем простой innerHTML, т.к. DOMParser не решил проблему
                element.innerHTML = translations[lang][key]; 
            } else {
                console.warn(`Translation key '${key}' not found for language '${lang}'.`);
            }
        });

        // Update page language attribute
        document.documentElement.lang = lang;

        // Update active button style
        langButtons.forEach(button => {
            if (button.getAttribute('data-lang') === lang) {
                button.classList.add('active');
            } else {
                button.classList.remove('active');
            }
        });

        // Save language preference
        try {
            localStorage.setItem('taleshift_lang', lang);
        } catch (e) {
            console.error('Failed to save language preference to localStorage:', e);
        }
    }

    function getInitialLanguage() {
        try {
            const savedLang = localStorage.getItem('taleshift_lang');
            if (savedLang && translations[savedLang]) {
                return savedLang;
            }
        } catch (e) {
            console.error('Failed to read language preference from localStorage:', e);
        }

        // Try browser language (get primary language code like 'en', 'ru')
        const browserLang = navigator.language ? navigator.language.split('-')[0] : 'en';
        if (translations[browserLang]) {
            return browserLang;
        }

        return 'en'; // Default to English
    }

    // Add event listeners to buttons
    langButtons.forEach(button => {
        button.addEventListener('click', () => {
            const lang = button.getAttribute('data-lang');
            setLanguage(lang);
        });
    });

    // Set initial language on page load
    const initialLang = getInitialLanguage();
    setLanguage(initialLang);

    // --- Cookie Notice Logic ---
    const cookieNotice = document.getElementById('cookie-notice');
    const cookieAcceptBtn = document.getElementById('cookie-accept');
    const cookieConsentKey = 'taleshift_cookie_consent';

    // Check if consent was already given
    try {
        if (!localStorage.getItem(cookieConsentKey) && cookieNotice) {
            // Use setTimeout to allow the page to render first, then slide in
            setTimeout(() => {
                cookieNotice.classList.add('show');
            }, 500); // Small delay before showing
        }
    } catch (e) {
        console.error('Failed to access localStorage for cookie consent:', e);
        // Still show the banner if localStorage fails
        if (cookieNotice) {
             setTimeout(() => {
                cookieNotice.classList.add('show');
            }, 500);
        }
    }

    // Add event listener to accept button
    if (cookieAcceptBtn && cookieNotice) {
        cookieAcceptBtn.addEventListener('click', () => {
            cookieNotice.classList.remove('show');
            // Optionally, add a class to smoothly transition out before setting display none or removing
            // For simplicity, we just hide it via transform now.
            
            // Save consent
            try {
                localStorage.setItem(cookieConsentKey, 'true');
            } catch (e) {
                console.error('Failed to save cookie consent to localStorage:', e);
            }
        });
    }

    // --- Burger menu logic --- 
    const burger = document.querySelector('.burger');
    const nav = document.querySelector('.nav');
    if (burger && nav) {
        burger.addEventListener('click', () => {
            // Toggle active class on nav menu to show/hide it
            nav.classList.toggle('nav--active'); 
            // Toggle active class on burger for styling (e.g., transform to 'X')
            burger.classList.toggle('burger--active'); 
        });

        // Optional: Close menu when a nav link is clicked
        const navLinks = nav.querySelectorAll('.nav__link');
        navLinks.forEach(link => {
            link.addEventListener('click', () => {
                if (nav.classList.contains('nav--active')) {
                    nav.classList.remove('nav--active');
                    burger.classList.remove('burger--active');
                }
            });
        });
    }
}); 