# Recipe Generator Mobile App

A modern, cross-platform mobile application for generating recipes using AI. Built with React Native, Expo, and Keycloak authentication.

## ✨ Features

- 🔐 **Keycloak Authentication** - Secure OAuth2/OIDC login
- 📝 **Multiple Recipe Generation Methods**:
  - Generate by text description
  - Generate from recipe URLs
  - Generate from food images
  - Generate from voice recordings
- 🌍 **Multi-language Support** - English and German
- ✏️ **AI-Powered Recipe Updates** - Refine recipes with natural language prompts
- 📱 **Cross-Platform** - iOS, Android, and Web from single codebase
- 💾 **Recipe Management** - Save, view, and delete your recipes
- 👤 **User Profile** - View your account information

## 🚀 Getting Started

### Prerequisites

- Node.js 16+ and npm/yarn
- Expo CLI: `npm install -g expo-cli`
- For iOS development: macOS with Xcode
- For Android development: Android Studio

### Installation

1. **Clone the repository**
   ```bash
   cd recipe-generator-mobile
   ```

2. **Install dependencies**
   ```bash
   npm install
   ```

3. **Configure environment**
   
   Update the constants in `src/constants/index.ts`:
   - `API_BASE_URL` - Your backend API URL
   - `KEYCLOAK_CONFIG` - Your Keycloak server configuration

4. **Start the development server**
   ```bash
   npm start
   ```

## 📱 Running the App

### Development Mode

```bash
# Start Expo dev server
npm start

# Run on iOS simulator
npm run ios

# Run on Android emulator
npm run android

# Run in web browser
npm run web
```

### Testing on Device

1. Install **Expo Go** app from App Store or Google Play
2. Scan the QR code from the terminal with your device camera
3. App will open in Expo Go

## 🏗️ Project Structure

```
recipe-generator-mobile/
├── src/
│   ├── components/         # Reusable UI components
│   │   └── Loading.tsx
│   ├── constants/          # App constants and configuration
│   │   └── index.ts
│   ├── hooks/              # Custom React hooks
│   ├── navigation/         # Navigation configuration
│   │   └── AppNavigator.tsx
│   ├── screens/            # Screen components
│   │   ├── LoginScreen.tsx
│   │   ├── GenerateScreen.tsx
│   │   ├── RecipesScreen.tsx
│   │   └── ProfileScreen.tsx
│   ├── services/           # API and auth services
│   │   ├── apiService.ts
│   │   └── authService.ts
│   └── types/              # TypeScript type definitions
│       └── index.ts
├── assets/                 # Images, fonts, etc.
├── App.tsx                 # Root component
└── package.json
```

## 🔧 Configuration

### API Configuration

Edit `src/constants/index.ts`:

```typescript
export const API_BASE_URL = __DEV__ 
  ? 'http://localhost:8080/api/v1'  // Development
  : 'https://your-api.com/api/v1';   // Production
```

### Keycloak Configuration

Update Keycloak settings in `src/constants/index.ts`:

```typescript
export const KEYCLOAK_CONFIG = {
  url: 'https://your-keycloak-server.com',
  realm: 'your-realm',
  clientId: 'your-client-id',
  redirectUri: 'com.yourapp://oauth/callback',
  scopes: ['openid', 'profile', 'email'],
};
```

## 📦 Building for Production

### iOS

```bash
# Build for iOS
expo build:ios

# Or using EAS Build (recommended)
eas build --platform ios
```

### Android

```bash
# Build for Android
expo build:android

# Or using EAS Build (recommended)
eas build --platform android
```

### Web

```bash
# Build web version
expo build:web

# Or
npm run web
```

## 🔑 Deep Linking Setup

For Keycloak OAuth to work on mobile, configure deep linking:

1. **iOS**: Add URL scheme in `app.json`
2. **Android**: Add intent filter in `app.json`

Example in `app.json`:
```json
{
  "expo": {
    "scheme": "com.recipegenerator",
    "ios": {
      "bundleIdentifier": "com.recipegenerator"
    },
    "android": {
      "package": "com.recipegenerator"
    }
  }
}
```

## 🛠️ Tech Stack

- **Framework**: React Native with Expo
- **Language**: TypeScript
- **Navigation**: React Navigation 6
- **Authentication**: React Native App Auth (OAuth2/OIDC)
- **HTTP Client**: Axios
- **State Management**: React Hooks
- **UI**: React Native core components
- **Storage**: AsyncStorage

## 📚 API Endpoints

The app integrates with these backend endpoints:

- `POST /api/v1/generate/by-description` - Generate from text
- `POST /api/v1/generate/by-link` - Generate from URL
- `POST /api/v1/generate/by-image` - Generate from image
- `POST /api/v1/generate/by-voice` - Generate from audio
- `POST /api/v1/update-recipe` - Update existing recipe
- `GET /api/v1/get-recipes` - Fetch all user recipes
- `DELETE /api/v1/delete-recipe/:id` - Delete a recipe

## 🧪 Testing

```bash
# Run tests
npm test

# Run with coverage
npm run test:coverage
```

## 🔐 Security Notes

- All API requests include Bearer token authentication
- Tokens are securely stored using AsyncStorage
- Automatic token refresh before expiration
- Secure OAuth2 flow with PKCE

## 🤝 Contributing

1. Fork the repository
2. Create your feature branch (`git checkout -b feature/AmazingFeature`)
3. Commit your changes (`git commit -m 'Add some AmazingFeature'`)
4. Push to the branch (`git push origin feature/AmazingFeature`)
5. Open a Pull Request

## 📄 License

This project is licensed under the MIT License.

## 🆘 Troubleshooting

### Keycloak Authentication Issues
- Verify redirectUri matches your app scheme
- Check Keycloak client configuration
- Ensure PKCE is enabled in Keycloak

### API Connection Issues
- Check API_BASE_URL in constants
- Verify backend is running and accessible
- Check CORS configuration on backend

### Build Issues
- Clear cache: `expo start -c`
- Remove node_modules: `rm -rf node_modules && npm install`
- Update Expo: `npm install expo@latest`

## 📞 Support

For issues and questions:
- Open an issue on GitHub
- Check Expo documentation: https://docs.expo.dev
- React Navigation docs: https://reactnavigation.org

---

Built with ❤️ using React Native and Expo
